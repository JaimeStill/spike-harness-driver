package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/template"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// ErrInvalid is returned, wrapped, for a workflow that can't run: a missing or duplicate name,
// a reference to a session or step it doesn't declare, a cycle, a prompt that isn't a valid
// template, or a schema that isn't an object schema.
var ErrInvalid = errors.New("workflow: invalid workflow")

// Workflow is a DAG of steps over named sessions.
type Workflow struct {
	Name     string        `json:"name"`
	Sessions []SessionSpec `json:"sessions"`
	Steps    []Step        `json:"steps"`
}

// SessionSpec names one session of a workflow and what it runs on. Its steps share the
// harness session, and so its history. Empty fields take the host's defaults; the Opener
// decides what each means.
type SessionSpec struct {
	Name     string `json:"name"`
	Harness  string `json:"harness,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Step is one exchange on one of the workflow's sessions.
type Step struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	// After names the steps that must end before this one starts.
	After []string `json:"after,omitempty"`
	// Prompt is a text/template whose data is a Context: the results of the steps this one
	// depends on, directly or through others.
	Prompt string `json:"prompt"`
	// Schema, when set, is the JSON Schema of the step's structured response, an object
	// schema, as harness.Request.Schema is.
	Schema json.RawMessage `json:"schema,omitempty"`
}

// Context is the data a step's prompt template runs on.
type Context struct {
	// Steps holds the result of every step the step depends on, by step ID.
	Steps map[string]StepResult
}

// StepResult is one step's result, as a prompt template sees it.
type StepResult struct {
	Text string
	// Structured is the step's structured response decoded from JSON, so a template reaches
	// into it by key, as {{.Steps.review.Structured.risk}}. It is nil for a step without one.
	Structured any
}

// Load decodes a workflow from JSON and validates it. Unknown fields are an error, so a
// misspelled key fails rather than being dropped.
func Load(r io.Reader) (Workflow, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var w Workflow
	if err := dec.Decode(&w); err != nil {
		return Workflow{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := w.Validate(); err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// Validate checks that w can run.
func (w Workflow) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...))
	}
	if w.Name == "" {
		fail("no name")
	}
	if len(w.Steps) == 0 {
		fail("no steps")
	}
	sessions := map[string]bool{}
	for _, s := range w.Sessions {
		switch {
		case s.Name == "":
			fail("a session has no name")
		case sessions[s.Name]:
			fail("session %q is declared twice", s.Name)
		}
		sessions[s.Name] = true
	}
	steps := map[string]bool{}
	for _, st := range w.Steps {
		switch {
		case st.ID == "":
			fail("a step has no id")
		case steps[st.ID]:
			fail("step %q is declared twice", st.ID)
		}
		steps[st.ID] = true
	}
	for _, st := range w.Steps {
		if !sessions[st.Session] {
			fail("step %q runs on undeclared session %q", st.ID, st.Session)
		}
		for _, dep := range st.After {
			switch {
			case dep == st.ID:
				fail("step %q comes after itself", st.ID)
			case !steps[dep]:
				fail("step %q comes after undeclared step %q", st.ID, dep)
			}
		}
		if strings.TrimSpace(st.Prompt) == "" {
			fail("step %q has no prompt", st.ID)
		} else if _, err := parse(st); err != nil {
			fail("step %q: %w", st.ID, err)
		}
		if st.Schema != nil {
			if err := harness.ValidateSchema(st.Schema); err != nil {
				fail("step %q: %w", st.ID, err)
			}
		}
	}
	if len(errs) == 0 {
		if cycle := w.cycle(); cycle != nil {
			fail("steps form a cycle: %s", strings.Join(cycle, " -> "))
		}
	}
	return errors.Join(errs...)
}

// Step returns the step id names.
func (w Workflow) Step(id string) (Step, bool) {
	i := slices.IndexFunc(w.Steps, func(s Step) bool { return s.ID == id })
	if i < 0 {
		return Step{}, false
	}
	return w.Steps[i], true
}

// Session returns the session name names.
func (w Workflow) Session(name string) (SessionSpec, bool) {
	i := slices.IndexFunc(w.Sessions, func(s SessionSpec) bool { return s.Name == name })
	if i < 0 {
		return SessionSpec{}, false
	}
	return w.Sessions[i], true
}

// Ancestors returns the IDs of the steps id depends on, directly or through others, in
// declaration order.
func (w Workflow) Ancestors(id string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		st, _ := w.Step(id)
		for _, dep := range st.After {
			if !seen[dep] {
				seen[dep] = true
				visit(dep)
			}
		}
	}
	visit(id)
	var ids []string
	for _, st := range w.Steps {
		if seen[st.ID] {
			ids = append(ids, st.ID)
		}
	}
	return ids
}

// Prompt renders step id's prompt over the results of the steps it depends on. A template that
// names a key its data doesn't hold is an error, so a misspelled step ID fails the step rather
// than sending "<no value>" to the model.
func (w Workflow) Prompt(id string, results map[string]harness.Result) (string, error) {
	st, ok := w.Step(id)
	if !ok {
		return "", fmt.Errorf("workflow: no step %q", id)
	}
	tmpl, err := parse(st)
	if err != nil {
		return "", err
	}
	data := Context{Steps: map[string]StepResult{}}
	for _, dep := range w.Ancestors(id) {
		res, ok := results[dep]
		if !ok {
			return "", fmt.Errorf("workflow: step %q: no result of step %q", id, dep)
		}
		sr := StepResult{Text: res.Text}
		if len(res.Structured) > 0 {
			if err := json.Unmarshal(res.Structured, &sr.Structured); err != nil {
				return "", fmt.Errorf("workflow: step %q: structured result: %w", dep, err)
			}
		}
		data.Steps[dep] = sr
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("workflow: step %q: %w", id, err)
	}
	return b.String(), nil
}

func parse(st Step) (*template.Template, error) {
	return template.New(st.ID).Option("missingkey=error").Parse(st.Prompt)
}

// cycle returns the steps of a cycle in the DAG, or nil when there is none.
func (w Workflow) cycle() []string {
	const (
		unseen = iota
		visiting
		done
	)
	mark := map[string]int{}
	var path []string
	var visit func(string) []string
	visit = func(id string) []string {
		switch mark[id] {
		case visiting:
			i := slices.Index(path, id)
			return append(slices.Clone(path[i:]), id)
		case done:
			return nil
		}
		mark[id] = visiting
		path = append(path, id)
		st, _ := w.Step(id)
		for _, dep := range st.After {
			if c := visit(dep); c != nil {
				return c
			}
		}
		path = path[:len(path)-1]
		mark[id] = done
		return nil
	}
	for _, st := range w.Steps {
		if c := visit(st.ID); c != nil {
			return c
		}
	}
	return nil
}
