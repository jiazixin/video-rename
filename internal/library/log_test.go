package library

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestBackgroundRefreshReportsFailure(t *testing.T) {
	t.Parallel()
	l, root, _ := fixture(t)
	var output bytes.Buffer
	l.logger = slog.New(slog.NewJSONHandler(&output, nil))
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	l.StartRefresh(t.Context(), 1)
	l.refreshWG.Wait()
	if !strings.Contains(output.String(), `"msg":"directory_refresh_failed"`) || !strings.Contains(output.String(), `"directory_id":1`) {
		t.Fatalf("got log %q, want refresh failure with directory ID", output.String())
	}
}
