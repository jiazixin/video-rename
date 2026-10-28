package app

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
)

// jsonRequest returns a test request whose body is sent as application/json.
func jsonRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/merge-patch+json")
	}
	if body == nil {
		return request
	}
	// Fixtures carry transport metadata alongside their payload for convenience.
	// Production requests use headers; contract rejection tests bypass this helper.
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		panic(err)
	}
	var object map[string]jsontext.Value
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(raw, &object) == nil {
		for _, field := range []string{"key", "request_key"} {
			if value, ok := object[field]; ok {
				var key string
				if json.Unmarshal(value, &key) == nil {
					request.Header.Set("Idempotency-Key", key)
					delete(object, field)
				}
			}
		}
		if value, ok := object["version"]; ok {
			var version int64
			if json.Unmarshal(value, &version) == nil {
				request.Header.Set("If-Match", entityTag(version))
				delete(object, "version")
			}
		}
		raw, err = json.Marshal(object)
		if err != nil {
			panic(err)
		}
	}
	request.Body = io.NopCloser(bytes.NewReader(raw))
	request.ContentLength = int64(len(raw))
	return request
}

// decodeItems decodes the items of a list response into out.
func decodeItems(body []byte, out any) error {
	var list struct {
		Items jsontext.Value `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return err
	}
	return json.Unmarshal(list.Items, out)
}
