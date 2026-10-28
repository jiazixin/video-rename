package app

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
)

func TestQuantitiesUseNumericUnitsAndOmitUnboundedLimits(t *testing.T) {
	c := newContractClient(t)
	r := c.call(contractRequest{method: "GET", path: "/api/sampling"})
	requireContractStatus(t, r, 200)
	var settings struct {
		Sampling struct {
			Duration []map[string]jsontext.Value `json:"duration_rules"`
			Size     []map[string]jsontext.Value `json:"size_rules"`
		} `json:"sampling"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	for key, rules := range map[string][]map[string]jsontext.Value{"max_duration_ms": settings.Sampling.Duration, "max_mib": settings.Sampling.Size} {
		if len(rules) < 2 {
			t.Fatalf("missing %s tiers", key)
		}
		var quantity int64
		if err := json.Unmarshal(rules[0][key], &quantity); err != nil || quantity <= 0 {
			t.Fatalf("invalid numeric %s: %s", key, r.Body.String())
		}
		if _, exists := rules[len(rules)-1][key]; exists {
			t.Fatalf("unbounded %s must omit its limit", key)
		}
	}
	r = c.call(contractRequest{method: "GET", path: "/api/model-configs/1"})
	requireContractStatus(t, r, 200)
	var profile map[string]jsontext.Value
	if err := json.Unmarshal(r.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	var timeout int64
	if err := json.Unmarshal(profile["timeout_ms"], &timeout); err != nil || timeout != 180000 {
		t.Fatalf("invalid timeout_ms: %s", r.Body.String())
	}
	if _, exists := profile["timeout"]; exists {
		t.Fatal("retired timeout field returned")
	}
	r = c.call(contractRequest{method: "POST", path: "/api/model-configs", body: `{"timeout":"180s"}`, key: "old-model-timeout"})
	requireContractStatus(t, r, 400)
	r = c.call(contractRequest{method: "PUT", path: "/api/sampling", body: `{"sampling":{"duration_rules":[{"max_duration":"30s","frames":8}]}}`})
	requireContractStatus(t, r, 400)
}
