package notifications

import (
	"math"
	"testing"
)

func TestHistoryPagination(t *testing.T) {
	s := service(t)
	for i := range 123 {
		_, err := s.DB.Exec("INSERT INTO notification_deliveries(kind,run,title,body,status,created,updated,next_attempt) VALUES('webhook',?,'title','body','sent','now','now','now')", i+1)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name               string
		page, size         int
		wantPage, wantSize int
		count, first       int
	}{
		{"first", 1, 50, 1, 50, 50, 123},
		{"second", 2, 50, 2, 50, 50, 73},
		{"last", 3, 50, 3, 50, 23, 23},
		{"hundred", 2, 100, 2, 100, 23, 23},
		{"two hundred", 1, 200, 1, 200, 123, 123},
		{"defaults", 0, 0, 1, 50, 50, 123},
		{"negative", -5, -10, 1, 50, 50, 123},
		{"overflow", math.MaxInt, 50, 3, 50, 23, 23},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListHistoryPage(t.Context(), tc.page, tc.size)
			if err != nil {
				t.Fatal(err)
			}
			if got.Total != 123 || got.Page != tc.wantPage || got.Size != tc.wantSize || len(got.Items) != tc.count || got.Items[0].Run != int64(tc.first) {
				t.Fatalf("unexpected page: %+v", got)
			}
			for i, item := range got.Items {
				if item.Run != int64(tc.first-i) {
					t.Fatalf("incorrect order at %d: %+v", i, item)
				}
			}
		})
	}
	if _, err := s.DB.Exec("DELETE FROM notification_deliveries WHERE run<=100"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListHistoryPage(t.Context(), 3, 50)
	if err != nil || got.Page != 1 || got.Total != 23 || len(got.Items) != 23 {
		t.Fatal("page did not shrink", got, err)
	}
	if _, err := s.DB.Exec("DELETE FROM notification_deliveries"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListHistoryPage(t.Context(), 99, 50)
	if err != nil || got.Page != 1 || got.Total != 0 || got.Items == nil || len(got.Items) != 0 {
		t.Fatal("incorrect empty page", got, err)
	}
}
