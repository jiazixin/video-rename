package app

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"video-rename/internal/domain"
)

var errPrecondition = &httpFailure{http.StatusPreconditionFailed, errorBody{Code: "precondition_failed", Message: "resource precondition does not match; refresh the resource"}}

func entityTag(version int64) string { return `"v` + strconv.FormatInt(version, 10) + `"` }

// matchVersion evaluates strong If-Match validators against the selected resource.
// The domain write still compares version atomically to prevent a read/write race.
func matchVersion(w http.ResponseWriter, r *http.Request, version int64) bool {
	for _, candidate := range strings.Split(r.Header.Get("If-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == entityTag(version) {
			return true
		}
	}
	writeError(w, r, errPrecondition)
	return false
}

// decodePatch applies RFC 7396 to a writable request projection. Read-only and
// unknown fields never enter that projection. Null removes a field, causing its
// zero value to be validated by the domain together with the retained fields.
func decodePatch(w http.ResponseWriter, r *http.Request, target any) bool {
	_, ok := decodePatchMembers(w, r, target)
	return ok
}
func decodePatchMembers(w http.ResponseWriter, r *http.Request, target any) (map[string]jsontext.Value, bool) {
	var patch map[string]jsontext.Value
	if !decode(w, r, &patch) {
		return nil, false
	}
	raw, err := json.Marshal(target)
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	var baseline map[string]jsontext.Value
	if err := json.Unmarshal(raw, &baseline); err != nil {
		writeError(w, r, err)
		return nil, false
	}
	for name := range patch {
		if _, exists := baseline[name]; !exists {
			writeError(w, r, domain.InvalidField(name, "unknown_field"))
			return nil, false
		}
	}
	merged, err := mergeObject(baseline, patch)
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	raw, err = json.Marshal(merged)
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	// Decode the merged document into a fresh projection so removed members,
	// including nested object members, are actually reset to zero values.
	value := reflect.ValueOf(target).Elem()
	fresh := reflect.New(value.Type())
	if err := json.Unmarshal(raw, fresh.Interface(), json.RejectUnknownMembers(true)); err != nil {
		writeError(w, r, domain.InvalidField("", "invalid_json"))
		return nil, false
	}
	value.Set(fresh.Elem())
	return patch, true
}

func mergeObject(target, patch map[string]jsontext.Value) (map[string]jsontext.Value, error) {
	for key, value := range patch {
		trimmed := bytes.TrimSpace(value)
		if bytes.Equal(trimmed, []byte("null")) {
			delete(target, key)
			continue
		}
		if len(trimmed) > 0 && trimmed[0] == '{' {
			nested := map[string]jsontext.Value{}
			if old := bytes.TrimSpace(target[key]); len(old) > 0 && old[0] == '{' {
				if err := json.Unmarshal(old, &nested); err != nil {
					return nil, err
				}
			}
			var change map[string]jsontext.Value
			if err := json.Unmarshal(value, &change); err != nil {
				return nil, err
			}
			result, err := mergeObject(nested, change)
			if err != nil {
				return nil, err
			}
			value, err = json.Marshal(result)
			if err != nil {
				return nil, err
			}
		}
		target[key] = value
	}
	return target, nil
}

func creationStatus(r *http.Request) int {
	if r.Method == http.MethodPost {
		return http.StatusCreated
	}
	return http.StatusOK
}

// decodeEmpty permits an absent body or an empty JSON object, never old fields.
func decodeEmpty(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	return decode(w, r, &struct{}{})
}
