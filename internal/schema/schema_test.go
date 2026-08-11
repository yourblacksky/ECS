package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDraft2020SchemaSupportsCanonicalContracts(t *testing.T) {
	document := decodeSchema(t, `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"$defs":{"identity":{"type":"string","pattern":"^u_[0-9]+$"}},
		"properties":{
			"kind":{"const":"meeting"},
			"owner":{"$ref":"#/$defs/identity"},
			"start_at":{"type":"string","format":"date-time"},
			"channel":{"anyOf":[{"const":"online"},{"const":"offline"}]}
		},
		"required":["kind","owner","start_at","channel"],
		"additionalProperties":false
	}`)
	if err := ValidateDefinition(document, true); err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"kind":"meeting","owner":"u_12","start_at":"2026-08-03T10:00:00+08:00","channel":"online"}`)
	if err := Validate(valid, document); err != nil {
		t.Fatalf("valid canonical input rejected: %v", err)
	}
	invalid := json.RawMessage(`{"kind":"meeting","owner":"alice","start_at":"tomorrow","channel":"phone"}`)
	if err := Validate(invalid, document); err == nil {
		t.Fatal("Draft 2020-12 constraints were not enforced")
	}
}

func TestDefinitionRejectsRemoteReference(t *testing.T) {
	document := decodeSchema(t, `{
		"type":"object",
		"properties":{"owner":{"$ref":"https://example.com/identity.json"}}
	}`)
	if err := ValidateDefinition(document, true); err == nil || !strings.Contains(err.Error(), "local fragment") {
		t.Fatalf("remote reference was not rejected: %v", err)
	}
}

func TestAdaptBuildsCanonicalInputAndReportsMissingSlots(t *testing.T) {
	model := decodeSchema(t, `{
		"type":"object",
		"properties":{"title":{"type":"string"},"start_time":{"type":"string"}},
		"required":["title","start_time"],
		"additionalProperties":false
	}`)
	canonical := decodeSchema(t, `{
		"type":"object",
		"properties":{
			"title":{"type":"string"},
			"schedule":{"type":"object","properties":{"start_at":{"type":"string"},"timezone":{"type":"string"}},"required":["start_at","timezone"],"additionalProperties":false},
			"execution":{"type":"object","properties":{"request_id":{"type":"string"}},"required":["request_id"],"additionalProperties":false}
		},
		"required":["title","schedule","execution"],
		"additionalProperties":false
	}`)
	adapter := Adapter{
		Version:  "ecs.adapter/v1",
		Mappings: map[string]string{"schedule.start_at": "start_time"},
		Defaults: map[string]any{"schedule.timezone": "Asia/Shanghai"},
		Context:  map[string]string{"execution.request_id": "request_id"},
	}
	result, missing, err := Adapt(
		json.RawMessage(`{"title":"复盘会","start_time":"2026-08-03 10:00"}`),
		model,
		canonical,
		adapter,
		RuntimeContext{RequestID: "req-1"},
	)
	if err != nil || len(missing) != 0 {
		t.Fatalf("adapt failed: missing=%v err=%v", missing, err)
	}
	for _, expected := range []string{`"start_at":"2026-08-03 10:00"`, `"timezone":"Asia/Shanghai"`, `"request_id":"req-1"`} {
		if !strings.Contains(string(result), expected) {
			t.Fatalf("canonical result missing %s: %s", expected, result)
		}
	}
	_, missing, err = Adapt(json.RawMessage(`{"title":"复盘会"}`), model, canonical, adapter, RuntimeContext{})
	if err != nil || len(missing) != 1 || missing[0] != "start_time" {
		t.Fatalf("missing slot not reported: missing=%v err=%v", missing, err)
	}
	_, missing, err = Adapt(
		json.RawMessage(`{"title":"复盘会","start_time":"2026-08-03 10:00"}`),
		model, canonical, adapter, RuntimeContext{},
	)
	if err == nil || len(missing) != 0 || !strings.Contains(err.Error(), "execution") {
		t.Fatalf("missing trusted context became a user slot: missing=%v err=%v", missing, err)
	}
}

func decodeSchema(t *testing.T, raw string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
