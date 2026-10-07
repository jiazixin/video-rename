package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadHandlersCancelWhilePoolIsExhausted(t *testing.T) {
	a := runsAnalyticsApp(t)
	var held []*sql.Conn
	for range 4 {
		conn, err := a.DB.Read().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for _, tc := range []struct {
		name, url string
		handler   http.HandlerFunc
	}{
		{"performance", "/api/runs/analytics", a.runsAnalytics},
		{"usage", "/api/usage/analytics", a.usageAnalytics},
		{"entries", "/api/entries/status?ids=500", a.entryStatus},
		{"items", "/api/runs/1/items?page=1", a.items},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			r := httptest.NewRequest("GET", tc.url, nil).WithContext(ctx)
			r.SetPathValue("id", "1")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { tc.handler(w, r); close(done) }()
			select {
			case <-done:
				if w.Code == 200 {
					t.Fatal("cancelled request returned success")
				}
			case <-time.After(time.Second):
				for _, conn := range held {
					conn.Close()
				}
				<-done
				t.Fatal("cancelled request waited for a pooled connection")
			}
		})
	}
}

func TestRunReadsDoNotWaitForWriter(t *testing.T) {
	a := runsAnalyticsApp(t)
	tx, err := a.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = a.Jobs.Get(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Jobs.List(ctx, 0); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/runs/1/items?page=1", nil).WithContext(ctx)
	r.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	a.items(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
