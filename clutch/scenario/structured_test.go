package scenario

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

func TestTheResponseSchemasAreObjectSchemas(t *testing.T) {
	for _, schema := range []json.RawMessage{capitalSchema, colorsSchema, greetingSchema} {
		var s struct {
			Type     string   `json:"type"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(schema, &s); err != nil || s.Type != "object" || len(s.Required) == 0 {
			t.Errorf("%s: %+v, %v", schema, s, err)
		}
	}
}

func TestDecode(t *testing.T) {
	var c capital
	if err := decode(json.RawMessage(`{"city":"Paris","country":"France"}`), &c); err != nil || c.City != "Paris" {
		t.Errorf("decode = %+v, %v", c, err)
	}
	if err := decode(nil, &c); err == nil {
		t.Error("no response decoded")
	}
	if err := decode(json.RawMessage(`{"city":1}`), &c); err == nil {
		t.Error("a mistyped response decoded")
	}
}

func TestNoteRetriesCountsFailedRespondResults(t *testing.T) {
	var out bytes.Buffer
	rep := NewReporter(output.New(&out, &out, nil))
	result := func(name string, failed bool) harness.Event {
		return harness.Event{Kind: harness.EventToolResult, Tool: &harness.ToolEvent{Name: name, IsError: failed}}
	}
	r := &run{events: []harness.Event{
		{Kind: harness.EventToolCall, Tool: &harness.ToolEvent{Name: respondTool}},
		result(respondTool, true),
		result("read", true),
		result(respondTool, true),
		result(respondTool, false),
	}}
	r.noteRetries(rep)
	if !strings.Contains(out.String(), "respond failed validation 2 time(s)") {
		t.Errorf("narration:\n%s", out.String())
	}

	out.Reset()
	r.events = []harness.Event{result(respondTool, false)}
	r.noteRetries(rep)
	if out.Len() != 0 {
		t.Errorf("a first-time response noted retries:\n%s", out.String())
	}
}
