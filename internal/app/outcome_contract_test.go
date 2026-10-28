package app

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"testing"

	"video-rename/internal/domain"
)

func TestOptionalOutcomeRecordsAreOmitted(t *testing.T) {
	c := newContractClient(t)
	if err := c.app.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,request_key)
 VALUES(1,'manual','[1]',0,'completed','2026-10-28T00:00:00.000Z','outcome-contract');
 INSERT INTO run_items(id,run,entry,identity,size,mtime_ns,status,original_path)
 VALUES(1,1,1,'fixture',0,0,'done','clip.mp4');
 INSERT INTO events(category,action,entry,run,old_path,created)
 VALUES('success','manual',1,1,'clip.mp4','2026-10-28T00:00:00.000Z')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []domain.Record{{}, {Code: "fixture_notice", Message: "fixture notice"}} {
		if err := c.app.db.Write(t.Context(), func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(t.Context(), "UPDATE run_items SET message=?", outcome.Text()); err != nil {
				return err
			}
			_, err := tx.ExecContext(t.Context(), "UPDATE events SET message=?", outcome.Text())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ path, list, field string }{
			{"/api/runs/1/items?status=done", "items", "message"},
			{"/api/runs/1/items?status=running", "recent", "message"},
			{"/api/logs?category=success", "items", "error"},
		} {
			response := c.call(contractRequest{method: http.MethodGet, path: tc.path})
			requireContractStatus(t, response, http.StatusOK)
			var fields map[string]jsontext.Value
			if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]jsontext.Value
			if err := json.Unmarshal(fields[tc.list], &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("%s: got %s, want one %s record", tc.path, response.Body.String(), tc.list)
			}
			raw, present := rows[0][tc.field]
			if present != (outcome.Code != "") {
				t.Fatalf("%s: got %s present=%v, want present=%v; response=%s", tc.path, tc.field, present, outcome.Code != "", response.Body.String())
			}
			if present {
				var got domain.Record
				if err := json.Unmarshal(raw, &got); err != nil || got.Code != outcome.Code {
					t.Fatalf("%s: got record=%s err=%v, want code=%q", tc.path, raw, err, outcome.Code)
				}
			}
		}
	}
}
