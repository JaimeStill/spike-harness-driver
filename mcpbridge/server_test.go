package mcpbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// echoSchema requires a string text, so a call without one fails validation.
var echoSchema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)

// answerSchema is a response schema; answerSchema2 is another.
var (
	answerSchema  = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"]}`)
	answerSchema2 = json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}},"required":["verdict"]}`)
)

// testTools are an echo tool, a tool whose handler fails, and a tool with no schema.
func testTools() []harness.Tool {
	return []harness.Tool{
		{
			Name:        "echo",
			Description: "Echoes text.",
			Schema:      echoSchema,
			Handler: func(_ context.Context, args json.RawMessage) (string, error) {
				var a struct{ Text string }
				if err := json.Unmarshal(args, &a); err != nil {
					return "", err
				}
				return "echo: " + a.Text, nil
			},
		},
		{
			Name:        "fail",
			Description: "Always fails.",
			Handler: func(context.Context, json.RawMessage) (string, error) {
				return "", errors.New("the tool broke")
			},
		},
	}
}

// recorder keeps what a server reports through its Options callbacks.
type recorder struct {
	mu         sync.Mutex
	structured []json.RawMessage
	rejected   []error
}

func (r *recorder) options() mcpbridge.Options {
	return mcpbridge.Options{
		OnStructured: func(v json.RawMessage) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.structured = append(r.structured, v)
		},
		OnRejected: func(err error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.rejected = append(r.rejected, err)
		},
	}
}

func (r *recorder) counts() (structured, rejected int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.structured), len(r.rejected)
}

func (r *recorder) lastRejected() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rejected) == 0 {
		return nil
	}
	return r.rejected[len(r.rejected)-1]
}

func TestNewRejects(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (string, error) { return "", nil }
	cases := map[string][]harness.Tool{
		"duplicate":       {{Name: "a", Handler: handler}, {Name: "a", Handler: handler}},
		"respond":         {{Name: "respond", Handler: handler}},
		"respond prefix":  {{Name: "respond_1234abcd", Handler: handler}},
		"respond-like":    {{Name: "responder", Handler: handler}},
		"no handler":      {{Name: "a"}},
		"bad name":        {{Name: "a b", Handler: handler}},
		"array schema":    {{Name: "a", Handler: handler, Schema: json.RawMessage(`{"type":"array"}`)}},
		"uncompilable":    {{Name: "a", Handler: handler, Schema: json.RawMessage(`{"type":"object","properties":{"x":{"pattern":"("}}}`)}},
		"not JSON schema": {{Name: "a", Handler: handler, Schema: json.RawMessage(`[1]`)}},
	}
	for name, tools := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := mcpbridge.New(tools, mcpbridge.Options{}); err == nil {
				t.Fatal("New succeeded")
			}
		})
	}
}

func TestIsRespond(t *testing.T) {
	for name, want := range map[string]bool{"respond_0011aabb": true, "respond": false, "echo": false} {
		if got := mcpbridge.IsRespond(name); got != want {
			t.Errorf("IsRespond(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSetSchema(t *testing.T) {
	s, err := mcpbridge.New(testTools(), mcpbridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.RespondName(); got != "" {
		t.Fatalf("RespondName before SetSchema = %q, want none", got)
	}
	for _, bad := range []string{`{"type":"string"}`, `{}`, `not json`, `{"type":"object","properties":{"x":{"pattern":"("}}}`} {
		if err := s.SetSchema(json.RawMessage(bad)); err == nil {
			t.Errorf("SetSchema(%s) succeeded", bad)
		}
	}
	if got := s.RespondName(); got != "" {
		t.Fatalf("RespondName after rejected schemas = %q, want none", got)
	}

	if err := s.SetSchema(answerSchema); err != nil {
		t.Fatal(err)
	}
	first := s.RespondName()
	if !mcpbridge.IsRespond(first) || len(first) != len(mcpbridge.RespondPrefix)+8 {
		t.Fatalf("RespondName = %q, want %s and 8 hex digits", first, mcpbridge.RespondPrefix)
	}

	// The same schema, differently spaced, keeps the name.
	spaced := json.RawMessage(strings.ReplaceAll(string(answerSchema), ",", ", "))
	if err := s.SetSchema(spaced); err != nil {
		t.Fatal(err)
	}
	if got := s.RespondName(); got != first {
		t.Fatalf("RespondName for the same schema = %q, want %q", got, first)
	}

	if err := s.SetSchema(answerSchema2); err != nil {
		t.Fatal(err)
	}
	if got := s.RespondName(); got == first || !mcpbridge.IsRespond(got) {
		t.Fatalf("RespondName for another schema = %q, want a respond name other than %q", got, first)
	}

	if err := s.SetSchema(nil); err != nil {
		t.Fatal(err)
	}
	if got := s.RespondName(); got != "" {
		t.Fatalf("RespondName after removal = %q, want none", got)
	}
}
