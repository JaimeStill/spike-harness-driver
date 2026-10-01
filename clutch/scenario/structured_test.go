package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestNoteRetriesCountsRejectedResponses(t *testing.T) {
	var out bytes.Buffer
	rep := NewReporter(output.New(&out, &out, nil))
	rejected := harness.Event{Kind: harness.EventStructuredRejected, Err: errors.New("missing city")}
	r := &run{events: []harness.Event{
		rejected,
		{Kind: harness.EventToolResult, Tool: &harness.ToolEvent{Name: "read", IsError: true}},
		rejected,
		{Kind: harness.EventStructured, Structured: json.RawMessage(`{}`)},
	}}
	r.noteRetries(rep)
	if !strings.Contains(out.String(), "failed validation 2 time(s)") {
		t.Errorf("narration:\n%s", out.String())
	}

	out.Reset()
	r.events = []harness.Event{{Kind: harness.EventStructured, Structured: json.RawMessage(`{}`)}}
	r.noteRetries(rep)
	if out.Len() != 0 {
		t.Errorf("a first-time response noted retries:\n%s", out.String())
	}
}
