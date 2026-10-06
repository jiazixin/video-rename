package vision

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestClientFailureKeywordsForBothProtocols(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, test := range []struct {
			name     string
			title    string
			keywords []string
			failed   bool
		}{
			{"defaults", "SORRY 无法回答", nil, true},
			{"custom sentence", "抱歉，这次我无法识别当前画面", []string{"无法识别当前画面"}, true},
			{"custom English sentence", "I CANNOT HELP WITH THAT task", []string{"I cannot help with that"}, true},
			{"removed marker", "命名失败", []string{"拒绝"}, false},
			{"disabled", "拒绝 命名失败 SORRY 无法完成", []string{}, false},
		} {
			t.Run(protocol+"/"+test.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]any
					if err := json.UnmarshalRead(r.Body, &body); err != nil {
						t.Error(err)
					}
					prompt := body["system"]
					if protocol == "openai" {
						prompt = body["messages"].([]any)[0].(map[string]any)["content"]
					}
					if prompt != "发送给模型的标题要求" {
						t.Error("failure keywords leaked into model prompt", prompt)
					}
					if _, ok := body["failure_keywords"]; ok {
						t.Error("failure keywords leaked into request")
					}
					if protocol == "anthropic" {
						content, _ := json.Marshal(test.title)
						fmt.Fprintf(w, `{"content":[{"type":"text","text":%s}],"stop_reason":"end_turn"}`, content)
					} else {
						fmt.Fprint(w, okResponse(test.title))
					}
				}))
				defer server.Close()
				cfg := testConfig(t, server.URL)
				cfg.Server.Protocol = protocol
				cfg.FailureKeywords = test.keywords
				client := newTestClient(cfg)
				defer client.Close()
				result, err := client.TitleOnce(t.Context(), "发送给模型的标题要求", frameSet(1))
				if calls != 1 {
					t.Fatal("client retried a failure title", calls)
				}
				if test.failed {
					if err == nil || err.Code != domain.CodeNamingFailed || err.Global || result.Title != "" || err.RawRequest == "" || !strings.Contains(err.RawResponse, test.title) {
						t.Fatalf("missing classification/diagnostics: %+v %+v", result, err)
					}
				} else if err != nil || result.Title != test.title {
					t.Fatalf("deleted or disabled rule still matched: %+v %+v", result, err)
				}
			})
		}
	}
}
