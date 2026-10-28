package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"testing"
)

func TestCollectRowsOwnsIterationAndClose(t *testing.T) {
	t.Parallel()
	scanFailure := errors.New("injected scan failure")
	readFailure := errors.New("injected iteration failure")
	closeFailure := errors.New("injected rows close failure")
	for _, tc := range []struct {
		name     string
		values   []int64
		scanErr  error
		readErr  error
		closeErr error
	}{
		{name: "values", values: []int64{1, 2}},
		{name: "empty"},
		{name: "scan failure", values: []int64{1, 2}, scanErr: scanFailure},
		{name: "iteration failure", values: []int64{1}, readErr: readFailure},
		{name: "close failure", values: []int64{1}, closeErr: closeFailure},
		{name: "scan and close failure", values: []int64{1, 2}, scanErr: scanFailure, closeErr: closeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := &fixtureRows{values: tc.values, readErr: tc.readErr, closeErr: tc.closeErr}
			db := sql.OpenDB(fixtureConnector{rows: source})
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			rows, err := db.QueryContext(t.Context(), "fixture rows")
			if err != nil {
				t.Fatal(err)
			}
			got, err := CollectRows(rows, func(rows *sql.Rows) (int64, error) {
				var value int64
				if err := rows.Scan(&value); err != nil {
					return 0, err
				}
				if value == 2 && tc.scanErr != nil {
					return 0, tc.scanErr
				}
				return value, nil
			})
			wantErr := errors.Join(tc.scanErr, tc.readErr, tc.closeErr)
			if wantErr == nil {
				if err != nil || got == nil || !slices.Equal(got, tc.values) {
					t.Fatalf("collected rows: got %v error=%v, want %v and nil", got, err, tc.values)
				}
			} else {
				if err == nil || got != nil {
					t.Fatalf("failed rows: got %v error=%v, want nil and failure", got, err)
				}
				for _, cause := range []error{tc.scanErr, tc.readErr, tc.closeErr} {
					if cause != nil && !errors.Is(err, cause) {
						t.Fatalf("collected error: got %v, want cause %v", err, cause)
					}
				}
			}
			if source.closed != 1 {
				t.Fatalf("rows close calls: got %d, want 1", source.closed)
			}
		})
	}
}

type fixtureConnector struct {
	rows     *fixtureRows
	closeErr error
}

func (c fixtureConnector) Connect(context.Context) (driver.Conn, error) {
	return fixtureConnection(c), nil
}

func (c fixtureConnector) Driver() driver.Driver {
	return fixtureDriver{}
}

type fixtureDriver struct{}

func (fixtureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fixture requires a connector")
}

type fixtureConnection fixtureConnector

func (c fixtureConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fixture does not support statements")
}

func (c fixtureConnection) Close() error {
	return c.closeErr
}

func (c fixtureConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("fixture does not support transactions")
}

func (c fixtureConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return c.rows, nil
}

type fixtureRows struct {
	values   []int64
	position int
	closed   int
	readErr  error
	closeErr error
}

func (r *fixtureRows) Columns() []string {
	return []string{"value"}
}

func (r *fixtureRows) Close() error {
	r.closed++
	return r.closeErr
}

func (r *fixtureRows) Next(dest []driver.Value) error {
	if r.position < len(r.values) {
		dest[0] = r.values[r.position]
		r.position++
		return nil
	}
	if r.readErr != nil {
		return r.readErr
	}
	return io.EOF
}
