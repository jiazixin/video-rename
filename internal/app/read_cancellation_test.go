package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"video-rename/internal/job"
)

func TestReadHandlersCancelWhilePoolIsExhausted(t *testing.T) {
	a := runsAnalyticsApp(t)
	release := make(chan struct{})
	var held sync.WaitGroup
	for range 4 {
		ready := make(chan struct{})
		held.Go(func() {
			if err := a.db.View(t.Context(), func(*sql.Tx) error {
				close(ready)
				<-release
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
		<-ready
	}
	defer func() { close(release); held.Wait() }()
	for _, tc := range []struct {
		name, url string
		handler   http.HandlerFunc
	}{
		{"performance", "/api/runs/analytics", a.runsAnalytics},
		{"usage", "/api/usage/analytics", a.usageAnalytics},
		{"entries", "/api/entries/status?ids=500", a.entryStatus},
		{"items", "/api/runs/1/items?offset=0", a.items},
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
				t.Fatal("cancelled request waited for a pooled connection")
			}
		})
	}
}

func TestRunReadsDoNotWaitForWriter(t *testing.T) {
	a := runsAnalyticsApp(t)
	if err := a.db.Write(t.Context(), func(*sql.Tx) error {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := a.Jobs.Get(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Jobs.ListPage(ctx, job.ListPageOptions{Category: "all"}); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/api/runs/1/items?offset=0", nil).WithContext(ctx)
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		a.items(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
