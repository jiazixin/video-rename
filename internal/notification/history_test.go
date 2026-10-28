package notification

import (
	"database/sql"
	"math"
	"testing"

	"video-rename/internal/storage"
)

func TestHistoryPagination(t *testing.T) {
	s := service(t)
	for i := range 123 {
		_, err := storage.WriteValue(t.Context(), s.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), "INSERT INTO notification_deliveries(kind,run,title,body,status,created,updated,next_attempt) VALUES('webhook',?,'title','body','sent','2026-10-28T00:00:00.000Z','2026-10-28T00:00:00.000Z','2026-10-28T00:00:00.000Z')", i+1)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name                 string
		offset, size         int
		wantOffset, wantSize int
		count, first         int
	}{
		{"first", 0, 50, 0, 50, 50, 123},
		{"second", 50, 50, 50, 50, 50, 73},
		{"last", 100, 50, 100, 50, 23, 23},
		{"hundred", 100, 100, 100, 100, 23, 23},
		{"two hundred", 0, 200, 0, 200, 123, 123},
		{"defaults", 0, 0, 0, 50, 50, 123},
		{"negative", -5, -10, 0, 50, 50, 123},
		{"overflow", math.MaxInt, 50, 100, 50, 23, 23},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListHistoryPage(t.Context(), tc.offset, tc.size)
			if err != nil {
				t.Fatal(err)
			}
			if got.Total != 123 || got.Offset != tc.wantOffset || got.Limit != tc.wantSize || len(got.Items) != tc.count || got.Items[0].Run != int64(tc.first) {
				t.Fatalf("unexpected page: %+v", got)
			}
			for i, item := range got.Items {
				if item.Run != int64(tc.first-i) {
					t.Fatalf("incorrect order at %d: %+v", i, item)
				}
			}
		})
	}
	if _, err := storage.WriteValue(t.Context(), s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "DELETE FROM notification_deliveries WHERE run<=100")
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListHistoryPage(t.Context(), 100, 50)
	if err != nil || got.Offset != 0 || got.Total != 23 || len(got.Items) != 23 {
		t.Fatal("page did not shrink", got, err)
	}
	if _, err := storage.WriteValue(t.Context(), s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "DELETE FROM notification_deliveries")
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListHistoryPage(t.Context(), 99, 50)
	if err != nil || got.Offset != 0 || got.Total != 0 || got.Items == nil || len(got.Items) != 0 {
		t.Fatal("incorrect empty page", got, err)
	}
}
