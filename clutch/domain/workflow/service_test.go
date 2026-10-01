package workflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	wfdomain "github.com/JaimeStill/spike-harness-driver/clutch/domain/workflow"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/harness"
	hfilestore "github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
	"github.com/JaimeStill/spike-harness-driver/workflow"
	"github.com/JaimeStill/spike-harness-driver/workflow/filestore"
)

// resumeHint is the line an interrupted run ends with.
var resumeHint = regexp.MustCompile(`resume with: clutch workflow resume (\S+)`)

// longPrompt streams until it is cancelled, while streaming is on.
const longPrompt = "long"

const pair = `{
	"name": "pair",
	"input": "Go",
	"sessions": [{"name": "a"}, {"name": "b"}],
	"steps": [
		{"id": "first", "session": "a", "prompt": "about {{.Input}}"},
		{"id": "second", "session": "b", "after": ["first"], "prompt": "%s {{.Steps.first.Text}}"}
	]
}`

// newService returns a Service whose sessions run on the scripted harness, with the run log and
// exchange records in state. With stream set, a prompt holding longPrompt streams until it is
// cancelled.
func newService(state string, stream bool) *wfdomain.Service {
	d := harnesstest.Driver{}
	if stream {
		d.Stream = longPrompt
	}
	records := hfilestore.New(filepath.Join(state, "exchanges"))
	open := func(ctx context.Context, spec workflow.SessionSpec, id string) (*harness.Session, error) {
		if id == "" {
			id = spec.Name + "-session"
		}
		return d.Open(ctx, harness.Options{SessionID: id, Store: records})
	}
	return wfdomain.New(func(limit int) (*workflow.Runner, error) {
		return workflow.NewRunner(open, filestore.New(filepath.Join(state, "workflows")), limit), nil
	})
}

func writeWorkflow(t *testing.T, second string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pair.json")
	if err := os.WriteFile(path, []byte(strings.Replace(pair, "%s", second, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func execute(t *testing.T, ctx context.Context, svc *wfdomain.Service, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := wfdomain.Commands(svc, output.New(&stdout, &stderr, nil))
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.ExecuteContext(ctx)
	return stdout.String(), err
}

func TestRunToTheEnd(t *testing.T) {
	state := t.TempDir()
	svc := newService(state, false)
	out, err := execute(t, t.Context(), svc, "run", writeWorkflow(t, "then"), "--limit", "1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"run_started", "a = harness session a-session", "first done: " + harnesstest.Reply, "second on b", "run_ended", "done: 2 of 2 steps done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	out, err = execute(t, t.Context(), svc, "list")
	if err != nil || !strings.Contains(out, "done      2/2") {
		t.Errorf("list: %v\n%s", err, out)
	}
}

func TestInterruptThenResume(t *testing.T) {
	state := t.TempDir()
	path := writeWorkflow(t, longPrompt)

	// The second step streams until the interrupt, which stops the run without ending it.
	ctx, cancel := context.WithCancel(t.Context())
	svc := newService(state, true)
	go func() {
		r, _ := svc.Runner(0)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			states, _ := r.Runs(t.Context())
			if len(states) == 1 && states[0].Steps["second"].Status == workflow.StatusRunning {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	out, err := execute(t, ctx, svc, "run", path)
	if err == nil || !strings.Contains(out, "resume with: clutch workflow resume ") {
		t.Fatalf("interrupted run: %v\n%s", err, out)
	}
	id := resumeHint.FindStringSubmatch(out)[1]

	out, err = execute(t, t.Context(), svc, "show", id)
	if err != nil {
		t.Fatal(err)
	}
	var s workflow.State
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if s.Status != workflow.StatusRunning || s.Steps["first"].Status != workflow.StatusDone {
		t.Fatalf("after the interrupt: %+v", s)
	}

	// A new process, whose harness answers, takes the run up: the first step keeps its result,
	// and only the second runs.
	out, err = execute(t, t.Context(), newService(state, false), "resume", id)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "resumed with 1 of 2 steps done") || strings.Contains(out, "first on a") || !strings.Contains(out, "second done") {
		t.Errorf("resume:\n%s", out)
	}
}

func TestCancelAnInterruptedRun(t *testing.T) {
	state := t.TempDir()
	svc := newService(state, true)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := execute(t, ctx, svc, "run", writeWorkflow(t, longPrompt)); err == nil {
		t.Fatal("the interrupted run succeeded")
	}
	out, _ := execute(t, t.Context(), svc, "list")
	id := strings.Fields(out)[0]
	if _, err := execute(t, t.Context(), svc, "cancel", id); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, t.Context(), svc, "resume", id); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Errorf("resuming a cancelled run: %v", err)
	}
}

func TestEventLine(t *testing.T) {
	tests := []struct {
		e    workflow.Event
		want string
	}{
		{workflow.Event{Seq: 4, Kind: workflow.KindStepEnded, Step: "s", Status: workflow.StatusDone, Adopted: true, Result: &harness.Result{Structured: json.RawMessage(`{"a": 1}`)}}, `#4   step_ended      s done (adopted from the session's records): {"a": 1}`},
		{workflow.Event{Kind: workflow.KindExchange, Step: "s", Exchange: &workflow.ExchangeEvent{Kind: harness.EventToolCall, Tool: "fingerprint"}}, "s tool_call fingerprint"},
		{workflow.Event{Kind: workflow.KindExchange, Exchange: &workflow.ExchangeEvent{Kind: harness.EventTextDelta, Text: "x"}}, ""},
	}
	for _, tt := range tests {
		got := wfdomain.EventLine(tt.e)
		if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
			t.Errorf("EventLine = %q, want it to hold %q", got, tt.want)
		}
	}
}

func TestExampleRenders(t *testing.T) {
	w, err := wfdomain.Load(filepath.Join("..", "..", "examples", "workflows", "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	review := harness.Result{Structured: json.RawMessage(`{"verdict": "revise", "risk": "high", "concerns": ["one", "two"]}`)}
	got, err := w.Prompt("decide", map[string]harness.Result{"security": review, "reliability": review, "operations": review})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "- reliability: revise, high risk. Concerns: one; two;") || !strings.Contains(got, "Redis Streams") {
		t.Errorf("decide's prompt:\n%s", got)
	}
}
