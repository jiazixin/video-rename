package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRequiresJSONObject(t *testing.T) {
	for _, body := range []string{"null", "[]", "true", "1", `"text"`, `{} {}`, `{"extra":1}`, `{"name":"a","name":"b"}`} {
		t.Run(body, func(t *testing.T) {
			var input struct {
				Name string `json:"name"`
			}
			w := httptest.NewRecorder()
			if decode(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), &input) || w.Code != 400 {
				t.Fatal("non-object or ambiguous input accepted", body, w.Code)
			}
		})
	}
}
