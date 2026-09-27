//go:build scale

package app

import (
	"context"
	"testing"
	"time"
)

func TestScaleBackgroundMarksDisconnected(t *testing.T) {
	a, ids := markTestApp(t, 10000)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := time.Now()
	markRequest(t, a, ctx, "scale-disconnected", ids, true, 202)
	t.Logf("submission returned in %s", time.Since(start))
	cancel()
	result := awaitMark(t, a, "scale-disconnected")
	if result.Status != "completed" || result.Changed != 20000 || result.Failed != 0 || result.Interrupted {
		t.Fatalf("incomplete after disconnect: %+v", result)
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM marks").Scan(&count); err != nil || count != 20000 {
		t.Fatal(count, err)
	}
	t.Logf("20,000 persisted marks after request cancellation in %s", time.Since(start))
}
