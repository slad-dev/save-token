package intelligent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRequestPreservesMessageExtraFields(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.4",
		"messages":[
			{
				"role":"assistant",
				"content":"tool call",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]
			},
			{
				"role":"tool",
				"content":"done",
				"tool_call_id":"call_1",
				"name":"lookup"
			}
		]
	}`)

	var request ChatRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	got := string(encoded)
	for _, needle := range []string{`"tool_calls"`, `"tool_call_id"`, `"name":"lookup"`} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected marshaled request to contain %s, got %s", needle, got)
		}
	}
}

func TestBuildSemanticQueryTextStripsOpenClawMetadata(t *testing.T) {
	input := strings.Join([]string{
		"Conversation info (untrusted metadata):",
		"```json",
		`{"conversation_label":"demo"}`,
		"```",
		"",
		"Sender (untrusted metadata):",
		"```json",
		`{"label":"tester"}`,
		"```",
		"",
		"请只回复 你好",
	}, "\n")

	got := buildSemanticQueryText(input)
	if got != "请只回复 你好" {
		t.Fatalf("expected semantic query to keep real user text, got %q", got)
	}
}
