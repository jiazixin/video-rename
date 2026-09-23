// Package tokenusage extracts a best-effort token count from saved model diagnostics.
package tokenusage

import "encoding/json/v2"

// Count prefers the provider's total usage. Without it, the existing local text
// estimate is used; image tokens and unrecorded retries cannot be reconstructed.
func Count(request, response string) (tokens int64, estimated bool, ok bool) {
	var result struct {
		Usage struct {
			Total         *int64 `json:"total_tokens"`
			Prompt        *int64 `json:"prompt_tokens"`
			Completion    *int64 `json:"completion_tokens"`
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			CacheCreation *int64 `json:"cache_creation_input_tokens"`
			CacheRead     *int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(response), &result) == nil {
		u := result.Usage
		if u.Total != nil && *u.Total >= 0 {
			return *u.Total, false, true
		}
		if u.Prompt != nil && u.Completion != nil && *u.Prompt >= 0 && *u.Completion >= 0 {
			return *u.Prompt + *u.Completion, false, true
		}
		if u.Input != nil && u.Output != nil && *u.Input >= 0 && *u.Output >= 0 {
			total := *u.Input + *u.Output
			for _, cached := range []*int64{u.CacheCreation, u.CacheRead} {
				if cached != nil && *cached > 0 {
					total += *cached
				}
			}
			return total, false, true
		}
	}
	var sent struct {
		Context struct {
			Text *int64 `json:"text_tokens_estimate"`
		} `json:"_local_context"`
	}
	if json.Unmarshal([]byte(request), &sent) == nil && sent.Context.Text != nil && *sent.Context.Text >= 0 {
		return *sent.Context.Text, true, true
	}
	return 0, false, false
}
