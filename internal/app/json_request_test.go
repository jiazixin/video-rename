package app

import (
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
