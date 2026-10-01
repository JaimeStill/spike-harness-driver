package workflow_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/workflow"
)

// review is a fan-out and fan-in: two reviewers, then a synthesis over both, then a follow-up
// on the synthesis session.
const review = `{
	"name": "review",
	"input": "the plan",
	"sessions": [{"name": "a"}, {"name": "b", "harness": "claude"}, {"name": "lead"}],
	"steps": [
		{"id": "ra", "session": "a", "prompt": "Review it.",
		 "schema": {"type": "object", "properties": {"risk": {"type": "string"}}}},
		{"id": "rb", "session": "b", "prompt": "Review it too."},
		{"id": "synth", "session": "lead", "after": ["ra", "rb"],
		 "prompt": "A says {{.Steps.ra.Structured.risk}}; B says {{.Steps.rb.Text}}."},
		{"id": "follow", "session": "lead", "after": ["synth"],
		 "prompt": "{{.Steps.ra.Structured.risk}} again: {{.Steps.synth.Text}} on {{.Input}}"}
	]
}`

func TestLoad(t *testing.T) {
	w, err := workflow.Load(strings.NewReader(review))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Steps) != 4 || len(w.Sessions) != 3 {
		t.Fatalf("loaded %d steps and %d sessions", len(w.Steps), len(w.Sessions))
	}
	if s, _ := w.Session("b"); s.Harness != "claude" {
		t.Errorf("session b's harness = %q", s.Harness)
	}
	if got := w.Ancestors("follow"); strings.Join(got, ",") != "ra,rb,synth" {
		t.Errorf("Ancestors(follow) = %v", got)
	}
}

func TestLoadUnknownField(t *testing.T) {
	_, err := workflow.Load(strings.NewReader(`{"name": "x", "stepz": []}`))
	if !errors.Is(err, workflow.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestValidate(t *testing.T) {
	sessions := []workflow.SessionSpec{{Name: "s"}}
	step := func(id string, after ...string) workflow.Step {
		return workflow.Step{ID: id, Session: "s", After: after, Prompt: "go"}
	}
	tests := []struct {
		name string
		w    workflow.Workflow
		want string
	}{
		{"no name", workflow.Workflow{Sessions: sessions, Steps: []workflow.Step{step("a")}}, "no name"},
		{"no steps", workflow.Workflow{Name: "w", Sessions: sessions}, "no steps"},
		{"duplicate session", workflow.Workflow{Name: "w", Sessions: append(sessions, sessions...), Steps: []workflow.Step{step("a")}}, `session "s" is declared twice`},
		{"duplicate step", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{step("a"), step("a")}}, `step "a" is declared twice`},
		{"undeclared session", workflow.Workflow{Name: "w", Steps: []workflow.Step{step("a")}}, `undeclared session "s"`},
		{"undeclared step", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{step("a", "z")}}, `undeclared step "z"`},
		{"self", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{step("a", "a")}}, "after itself"},
		{"cycle", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{step("a", "c"), step("b", "a"), step("c", "b")}}, "cycle: a -> c -> b -> a"},
		{"template", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{{ID: "a", Session: "s", Prompt: "{{.Steps"}}}, `step "a": template`},
		{"empty prompt", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{{ID: "a", Session: "s", Prompt: " "}}}, "no prompt"},
		{"schema", workflow.Workflow{Name: "w", Sessions: sessions, Steps: []workflow.Step{{ID: "a", Session: "s", Prompt: "go", Schema: json.RawMessage(`{"type":"string"}`)}}}, "invalid schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.w.Validate()
			if !errors.Is(err, workflow.ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want ErrInvalid containing %q", err, tt.want)
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	w, err := workflow.Load(strings.NewReader(review))
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]harness.Result{
		"ra":    {Structured: json.RawMessage(`{"risk": "high"}`)},
		"rb":    {Text: "fine"},
		"synth": {Text: "mixed"},
	}
	got, err := w.Prompt("follow", results)
	if err != nil {
		t.Fatal(err)
	}
	if got != "high again: mixed on the plan" {
		t.Errorf("Prompt(follow) = %q", got)
	}
	if _, err := w.Prompt("synth", map[string]harness.Result{"ra": results["ra"]}); err == nil || !strings.Contains(err.Error(), `no result of step "rb"`) {
		t.Errorf("Prompt without rb: err = %v", err)
	}
}

func TestPromptMissingKey(t *testing.T) {
	w := workflow.Workflow{
		Name:     "w",
		Sessions: []workflow.SessionSpec{{Name: "s"}},
		Steps: []workflow.Step{
			{ID: "a", Session: "s", Prompt: "go"},
			{ID: "b", Session: "s", After: []string{"a"}, Prompt: "{{.Steps.typo.Text}}"},
		},
	}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prompt("b", map[string]harness.Result{"a": {Text: "x"}}); err == nil {
		t.Fatal("a template naming a step outside the step's ancestors rendered")
	}
}
