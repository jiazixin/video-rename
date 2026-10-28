package tokenusage

import (
	"encoding/json/v2"
	"math"
)

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
		if u.Prompt != nil && u.Completion != nil {
			if total, valid := sumUsage(u.Prompt, u.Completion); valid {
				return total, false, true
			}
		}
		if u.Input != nil && u.Output != nil {
			if total, valid := sumUsage(u.Input, u.Output, u.CacheCreation, u.CacheRead); valid {
				return total, false, true
			}
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

// A malformed provider count must not wrap into a negative persisted total.
func sumUsage(values ...*int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value == nil {
			continue
		}
		if *value < 0 || *value > math.MaxInt64-total {
			return 0, false
		}
		total += *value
	}
	return total, true
}
