package app

import (
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"video-rename/internal/config"
	modelstore "video-rename/internal/model"
)

type contractClient struct {
	t       *testing.T
	app     *App
	handler http.Handler
	cookie  *http.Cookie
}
type contractRequest struct{ method, path, body, tag, key string }

func newContractClient(t *testing.T) contractClient {
	t.Helper()
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "contract-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdown(t, a) })
	c := contractClient{t: t, app: a, handler: a.Handler()}
	w := c.call(contractRequest{method: "POST", path: "/api/auth/login", body: `{"username":"admin","password":"contract-password"}`})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	c.cookie = w.Result().Cookies()[0]
	return c
}

// call deliberately sends the body unchanged, including invalid or retired fields.
func (c contractClient) call(input contractRequest) *httptest.ResponseRecorder {
	c.t.Helper()
	r := httptest.NewRequest(input.method, input.path, strings.NewReader(input.body))
	r.Header.Set("Content-Type", "application/json")
	if input.method == "PATCH" {
		r.Header.Set("Content-Type", "application/merge-patch+json")
	}
	if input.tag != "" {
		r.Header.Set("If-Match", input.tag)
	}
	if input.key != "" {
		r.Header.Set("Idempotency-Key", input.key)
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	return w
}
func requireContractStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body.String())
	}
}

func TestNumericQueryRejectsMalformedValues(t *testing.T) {
	c := newContractClient(t)
	for _, field := range []string{"page", "size"} {
		for _, value := range []string{"", "invalid", "1.5", "9223372036854775808"} {
			t.Run(field+"="+value, func(t *testing.T) {
				query := url.Values{field: {value}}
				response := c.call(contractRequest{method: http.MethodGet, path: "/api/notifications/history?" + query.Encode()})
				requireContractStatus(t, response, http.StatusBadRequest)
				var payload struct {
					Error struct {
						Code    string `json:"code"`
						Details struct {
							Fields []struct {
								Path   string `json:"path"`
								Reason string `json:"reason"`
							} `json:"fields"`
						} `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				fields := payload.Error.Details.Fields
				if payload.Error.Code != "invalid_argument" || len(fields) != 1 || fields[0].Path != field || fields[0].Reason != "invalid_number" {
					t.Fatalf("got invalid-argument response %s for %s", response.Body.String(), query.Encode())
				}
			})
		}
	}
}

func TestMergePatchPreconditionsAndRetiredFields(t *testing.T) {
	c := newContractClient(t)
	created := c.call(contractRequest{method: "POST", path: "/api/prompts", body: `{"title":"initial","body":"keep this content"}`, key: "prompt-create-1"})
	requireContractStatus(t, created, 201)
	location, tag := created.Header().Get("Location"), created.Header().Get("ETag")
	if location == "" || tag == "" {
		t.Fatal("creation omitted Location or ETag")
	}
	patch := contractRequest{method: "PATCH", path: location, body: `{"title":"changed"}`, tag: tag}
	updated := c.call(patch)
	requireContractStatus(t, updated, 200)
	if !strings.Contains(updated.Body.String(), `"body":"keep this content"`) || updated.Header().Get("ETag") == tag {
		t.Fatal(updated.Body.String(), updated.Header())
	}
	requireContractStatus(t, c.call(patch), 412)
	patch.tag = ""
	requireContractStatus(t, c.call(patch), 412)
	patch.tag = updated.Header().Get("ETag")
	for _, body := range []string{`{"version":2}`, `{"unknown":true}`, `{"key":"retired-key"}`, `{"id":1}`, `{"body":null}`} {
		patch.body = body
		requireContractStatus(t, c.call(patch), 400)
	}
	current := c.call(contractRequest{method: "GET", path: location})
	requireContractStatus(t, current, 200)
	if current.Header().Get("ETag") != patch.tag {
		t.Fatal("rejected patch changed resource")
	}
	// A weak validator cannot satisfy a strong If-Match condition.
	requireContractStatus(t, c.call(contractRequest{method: "DELETE", path: location, tag: "W/" + patch.tag}), 412)
	requireContractStatus(t, c.call(contractRequest{method: "DELETE", path: location, tag: patch.tag, body: `{"version":2}`}), 400)
	requireContractStatus(t, c.call(contractRequest{method: "DELETE", path: location, tag: patch.tag, body: `{}`}), 204)
}
func TestModelAndScheduleMergePatch(t *testing.T) {
	c := newContractClient(t)
	model := c.call(contractRequest{method: "POST", path: "/api/model-configs", key: "model-create-1", body: `{"title":"model","prompt_id":1,"provider":"custom","protocol":"openai","base_url":"http://localhost/v1","model":"fixture","api_key":"private-secret","timeout":"10s","max_tokens":100,"temperature":0.7}`})
	requireContractStatus(t, model, 201)
	location := model.Header().Get("Location")
	unchanged := c.call(contractRequest{method: "PATCH", path: location, tag: model.Header().Get("ETag"), body: `{"title":"renamed"}`})
	requireContractStatus(t, unchanged, 200)
	var profile modelstore.Profile
	if err := json.Unmarshal(unchanged.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if !profile.HasAPIKey || profile.Model != "fixture" || strings.Contains(unchanged.Body.String(), "private-secret") {
		t.Fatal(unchanged.Body.String())
	}
	cleared := c.call(contractRequest{method: "PATCH", path: location, tag: unchanged.Header().Get("ETag"), body: `{"api_key":null,"temperature":0}`})
	requireContractStatus(t, cleared, 200)
	stored, err := modelstore.New(c.app.db).Get(t.Context(), profile.ID)
	if err != nil || stored.HasAPIKey || stored.Temperature != 0 {
		t.Fatal(stored.HasAPIKey, stored.Temperature, err)
	}
	plan := c.call(contractRequest{method: "POST", path: "/api/schedules", key: "schedule-create-1", body: `{"name":"scan","task":"extract","targets":[1],"recursive":true,"kind":"daily","expression":"0 0 * * *","timezone":"UTC","enabled":true}`})
	requireContractStatus(t, plan, 201)
	changed := c.call(contractRequest{method: "PATCH", path: plan.Header().Get("Location"), tag: plan.Header().Get("ETag"), body: `{"enabled":false}`})
	requireContractStatus(t, changed, 200)
	var got map[string]any
	if err := json.Unmarshal(changed.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["enabled"] != false || got["name"] != "scan" || got["recursive"] != true {
		t.Fatal(got)
	}
	requireContractStatus(t, c.call(contractRequest{method: "DELETE", path: plan.Header().Get("Location"), tag: plan.Header().Get("ETag")}), 412)
}
func TestConcurrentCreationIdempotency(t *testing.T) {
	c := newContractClient(t)
	for _, input := range []contractRequest{
		{method: "POST", path: "/api/prompts", key: "parallel-prompt", body: `{"title":"one prompt","body":"one body"}`},
		{method: "POST", path: "/api/model-configs/1/copy", key: "parallel-model-copy", body: `{}`},
		{method: "POST", path: "/api/schedules", key: "parallel-schedule", body: `{"name":"one schedule","task":"extract","targets":[1],"kind":"daily","expression":"0 0 * * *","timezone":"UTC"}`},
	} {
		t.Run(input.path, func(t *testing.T) {
			results := make([]*httptest.ResponseRecorder, 8)
			var work sync.WaitGroup
			for i := range results {
				work.Go(func() { results[i] = c.call(input) })
			}
			work.Wait()
			for _, result := range results {
				requireContractStatus(t, result, 201)
				if result.Body.String() != results[0].Body.String() || result.Header().Get("ETag") != results[0].Header().Get("ETag") || result.Header().Get("Location") != results[0].Header().Get("Location") {
					t.Fatal("replay changed original response")
				}
			}
			input.body = `{"different":true}`
			conflict := c.call(input)
			requireContractStatus(t, conflict, 409)
			if !strings.Contains(conflict.Body.String(), "idempotency_key_reused") {
				t.Fatal(conflict.Body.String())
			}
		})
	}
	list, err := modelstore.New(c.app.db).List(t.Context())
	if err != nil || len(list) != 2 {
		t.Fatalf("copy duplicated resource: models=%d error=%v", len(list), err)
	}
}

func TestIdempotencyRetainsEmptyResponses(t *testing.T) {
	c := newContractClient(t)
	request := contractRequest{method: http.MethodPost, path: "/api/directories/1/refresh", body: `{}`, key: "empty-response"}
	for range 2 {
		response := c.call(request)
		requireContractStatus(t, response, http.StatusAccepted)
		if response.Body.Len() != 0 {
			t.Fatalf("got body %q, want empty 202 response", response.Body.String())
		}
	}
}

func TestIdempotencyRetainsMethodAllowance(t *testing.T) {
	c := newContractClient(t)
	request := contractRequest{method: http.MethodPost, path: "/api/system", body: `{}`, key: "unsupported-method"}
	var original string
	for range 2 {
		response := c.call(request)
		requireContractStatus(t, response, http.StatusMethodNotAllowed)
		if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Fatalf("got Allow %q, want GET, HEAD", allow)
		}
		if !strings.Contains(response.Body.String(), `"method_not_allowed"`) {
			t.Fatalf("got error response %s, want method_not_allowed", response.Body.String())
		}
		if original != "" && original != response.Body.String() {
			t.Fatal("replayed response differs from the original JSON body")
		}
		original = response.Body.String()
	}
}

func TestMergePatchNestedMembersAndArrays(t *testing.T) {
	type nested struct {
		Text    string `json:"text"`
		Enabled bool   `json:"enabled"`
	}
	value := struct {
		Nested nested `json:"nested"`
		Items  []int  `json:"items"`
	}{nested{"keep", true}, []int{1, 2}}
	r := httptest.NewRequest("PATCH", "/test", strings.NewReader(`{"nested":{"enabled":null},"items":[3]}`))
	r.Header.Set("Content-Type", "application/merge-patch+json")
	w := httptest.NewRecorder()
	if !decodePatch(w, r, &value) || value.Nested.Text != "keep" || value.Nested.Enabled || fmt.Sprint(value.Items) != "[3]" {
		t.Fatal(value, w.Body.String())
	}
}
