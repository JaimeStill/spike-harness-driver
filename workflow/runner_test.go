package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/workflow"
)

// script is how the fake harness answers one prompt.
type script struct {
	reply      string
	structured json.RawMessage
	// gate, when set, holds the run until it closes or the run is cancelled.
	gate chan struct{}
	err  error
}

// fakeHarness opens sessions over scripted connections, keeps their exchange records, and
// counts the prompts in flight.
type fakeHarness struct {
	script func(req harness.Request) script
	store  *recordStore

	mu       sync.Mutex
	next     int
	opened   []string // "<session>=<recorded harness ID>", per Open
	prompts  []string
	inflight int
	peak     int
	closed   int
	conns    int
}

func newFakeHarness(s func(req harness.Request) script) *fakeHarness {
	return &fakeHarness{script: s, store: &recordStore{recs: map[string][]harness.Record{}}}
}

func (h *fakeHarness) open(ctx context.Context, spec workflow.SessionSpec, id string) (*harness.Session, error) {
	h.mu.Lock()
	h.opened = append(h.opened, spec.Name+"="+id)
	if id == "" {
		h.next++
		id = fmt.Sprintf("%s-%d", spec.Name, h.next)
	}
	h.conns++
	h.mu.Unlock()
	c := &fakeConn{h: h, events: make(chan harness.Event, 64), done: make(chan struct{})}
	return harness.NewSession(ctx, id, c, h.store)
}

func (h *fakeHarness) count(prompt string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.prompts {
		if strings.Contains(p, prompt) {
			n++
		}
	}
	return n
}

type fakeConn struct {
	h      *fakeHarness
	events chan harness.Event
	done   chan struct{}
	runs   sync.WaitGroup

	mu        sync.Mutex
	cancel    chan struct{}
	closeOnce sync.Once
}

func (c *fakeConn) Prompt(_ context.Context, req harness.Request) error {
	h := c.h
	h.mu.Lock()
	h.prompts = append(h.prompts, req.Text)
	h.inflight++
	h.peak = max(h.peak, h.inflight)
	h.mu.Unlock()
	cancel := make(chan struct{})
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	s := h.script(req)
	c.runs.Add(1)
	go func() {
		defer c.runs.Done()
		c.run(req, s, cancel)
	}()
	return nil
}

func (c *fakeConn) run(req harness.Request, s script, cancel chan struct{}) {
	ended := func(evs ...harness.Event) {
		c.h.mu.Lock()
		c.h.inflight--
		c.h.mu.Unlock()
		for _, ev := range append(evs, harness.Event{Kind: harness.EventEnded}) {
			select {
			case c.events <- ev:
			case <-c.done:
				return
			}
		}
	}
	c.events <- harness.Event{Kind: harness.EventStarted}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-cancel:
			ended(harness.Event{Kind: harness.EventMessageEnd, StopReason: "aborted"}, harness.Event{Kind: harness.EventCancelled, StopReason: "aborted"})
			return
		case <-c.done:
			ended()
			return
		}
	}
	if s.err != nil {
		ended(harness.Event{Kind: harness.EventError, Err: s.err}, harness.Event{Kind: harness.EventMessageEnd, StopReason: "error"})
		return
	}
	reply := s.reply
	if reply == "" {
		reply = "ok"
	}
	evs := []harness.Event{{Kind: harness.EventTextDelta, Text: reply}}
	if req.Schema != nil {
		evs = append(evs, harness.Event{Kind: harness.EventStructured, Structured: s.structured})
	}
	ended(append(evs, harness.Event{Kind: harness.EventMessageEnd, Text: reply, StopReason: "stop"})...)
}

func (c *fakeConn) Cancel(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		close(c.cancel)
		c.cancel = nil
	}
	return nil
}

func (c *fakeConn) Events() <-chan harness.Event { return c.events }
func (c *fakeConn) Err() error                   { return nil }

func (c *fakeConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.runs.Wait()
		close(c.events)
		c.h.mu.Lock()
		c.h.closed++
		c.h.mu.Unlock()
	})
	return nil
}

// recordStore is a harness.Store in memory.
type recordStore struct {
	mu   sync.Mutex
	recs map[string][]harness.Record
}

func (s *recordStore) Put(_ context.Context, rec harness.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.SessionID] = append(s.recs[rec.SessionID], rec)
	return nil
}

func (s *recordStore) Records(_ context.Context, id string) ([]harness.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.recs[id]), nil
}

// logStore is a workflow.Store in memory. fail, when set, refuses the events it matches.
type logStore struct {
	mu   sync.Mutex
	logs map[string][]workflow.Event
	fail func(workflow.Event) bool
}

func newLogStore() *logStore { return &logStore{logs: map[string][]workflow.Event{}} }

func (s *logStore) Append(_ context.Context, e workflow.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil && s.fail(e) {
		return errors.New("disk full")
	}
	s.logs[e.RunID] = append(s.logs[e.RunID], e)
	return nil
}

func (s *logStore) Events(_ context.Context, id string, after int) ([]workflow.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []workflow.Event
	for _, e := range s.logs[id] {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *logStore) Runs(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.logs {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *logStore) kinds(id string) []workflow.Kind {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kinds []workflow.Kind
	for _, e := range s.logs[id] {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func load(t *testing.T, src string) workflow.Workflow {
	t.Helper()
	w, err := workflow.Load(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func wait(t *testing.T, r *workflow.Runner, id string) workflow.State {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := r.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return s
}

// until subscribes to the run and returns once an event matches.
func until(t *testing.T, r *workflow.Runner, id string, match func(workflow.Event) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := r.Subscribe(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for e := range events {
		if match(e) {
			return
		}
	}
	t.Fatal("no matching event")
}

func started(step string) func(workflow.Event) bool {
	return func(e workflow.Event) bool { return e.Kind == workflow.KindStepStarted && e.Step == step }
}

func TestRunFanOutFanIn(t *testing.T) {
	h := newFakeHarness(func(req harness.Request) script {
		return script{reply: "B is fine", structured: json.RawMessage(`{"risk": "high"}`)}
	})
	r := workflow.NewRunner(h.open, newLogStore(), 0)
	id, err := r.Start(context.Background(), load(t, review))
	if err != nil {
		t.Fatal(err)
	}
	s := wait(t, r, id)
	if s.Status != workflow.StatusDone || s.Done() != 4 {
		t.Fatalf("state = %+v", s)
	}
	if h.count("A says high; B says B is fine.") != 1 || h.count("high again: B is fine on the plan") != 1 {
		t.Errorf("prompts = %q", h.prompts)
	}
	// One open per session, the lead session's two steps sharing one, and each closed again.
	if len(h.opened) != 3 || h.closed != 3 {
		t.Errorf("opened %v, closed %d", h.opened, h.closed)
	}
	if s.Sessions["lead"] == "" || s.Steps["follow"].Result.Text != "B is fine" {
		t.Errorf("sessions %v, follow %+v", s.Sessions, s.Steps["follow"])
	}
}

// parallel is four independent steps on four sessions.
const parallel = `{
	"name": "parallel",
	"sessions": [{"name": "a"}, {"name": "b"}, {"name": "c"}, {"name": "d"}],
	"steps": [
		{"id": "a", "session": "a", "prompt": "a"},
		{"id": "b", "session": "b", "prompt": "b"},
		{"id": "c", "session": "c", "prompt": "c"},
		{"id": "d", "session": "d", "prompt": "d"}
	]
}`

func TestRunLimit(t *testing.T) {
	gate := make(chan struct{})
	h := newFakeHarness(func(harness.Request) script { return script{gate: gate} })
	r := workflow.NewRunner(h.open, newLogStore(), 2)
	id, err := r.Start(context.Background(), load(t, parallel))
	if err != nil {
		t.Fatal(err)
	}
	// Give the scheduler time to overrun the limit, if it would.
	deadline := time.Now().Add(5 * time.Second)
	for h.count("") < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := h.count(""); n != 2 {
		t.Fatalf("%d prompts in flight under a limit of 2", n)
	}
	// The slots go to the steps in declaration order.
	if h.count("a") != 1 || h.count("b") != 1 || len(h.opened) != 2 {
		t.Errorf("prompts %q, opened %v", h.prompts, h.opened)
	}
	close(gate)
	if s := wait(t, r, id); s.Status != workflow.StatusDone {
		t.Fatalf("status %s", s.Status)
	}
	if h.peak != 2 {
		t.Errorf("peak %d exchanges in flight, want 2", h.peak)
	}
}

func TestRunCancel(t *testing.T) {
	h := newFakeHarness(func(harness.Request) script { return script{gate: make(chan struct{})} })
	store := newLogStore()
	r := workflow.NewRunner(h.open, store, 0)
	id, err := r.Start(context.Background(), load(t, review))
	if err != nil {
		t.Fatal(err)
	}
	until(t, r, id, started("rb"))
	if err := r.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	s := wait(t, r, id)
	if s.Status != workflow.StatusCancelled || s.Steps["rb"].Status != workflow.StatusCancelled || s.Steps["synth"].Status != workflow.StatusPending {
		t.Fatalf("state = %+v", s)
	}
	if h.closed != h.conns {
		t.Errorf("closed %d of %d sessions", h.closed, h.conns)
	}
	if err := r.Cancel(context.Background(), id); !errors.Is(err, workflow.ErrRunEnded) {
		t.Errorf("cancelling an ended run: err = %v", err)
	}
}

func TestRunFailure(t *testing.T) {
	h := newFakeHarness(func(req harness.Request) script {
		if req.Text == "Review it." {
			return script{err: errors.New("provider exploded")}
		}
		return script{gate: make(chan struct{})}
	})
	r := workflow.NewRunner(h.open, newLogStore(), 0)
	id, err := r.Start(context.Background(), load(t, review))
	if err != nil {
		t.Fatal(err)
	}
	s := wait(t, r, id)
	if s.Status != workflow.StatusFailed || !strings.Contains(s.Err, "provider exploded") {
		t.Fatalf("state = %+v", s)
	}
	if s.Steps["ra"].Status != workflow.StatusFailed || s.Steps["rb"].Status != workflow.StatusCancelled {
		t.Errorf("steps = %+v", s.Steps)
	}
}

func TestResume(t *testing.T) {
	// The first runner's log refuses ra's end: the run stops as a crash would, after ra's
	// exchange ended and the harness recorded it, but before the log said so. rb is still in
	// flight.
	gate := make(chan struct{})
	h := newFakeHarness(func(req harness.Request) script {
		if req.Text == "Review it too." {
			return script{gate: gate}
		}
		return script{reply: "fine", structured: json.RawMessage(`{"risk": "low"}`)}
	})
	store := newLogStore()
	store.fail = func(e workflow.Event) bool { return e.Kind == workflow.KindStepEnded && e.Step == "ra" }
	r1 := workflow.NewRunner(h.open, store, 0)
	id, err := r1.Start(context.Background(), load(t, review))
	if err != nil {
		t.Fatal(err)
	}
	until(t, r1, id, started("rb"))
	s, err := r1.Wait(context.Background(), id)
	if err == nil || s.Status != workflow.StatusRunning {
		t.Fatalf("first runner: %v, state %+v", err, s)
	}
	recorded := s.Sessions["a"]
	close(gate)

	store.fail = nil
	r2 := workflow.NewRunner(h.open, store, 0)
	resumed, err := r2.ResumeAll(context.Background())
	if err != nil || len(resumed) != 1 {
		t.Fatalf("ResumeAll = %v, %v", resumed, err)
	}
	s = wait(t, r2, id)
	if s.Status != workflow.StatusDone {
		t.Fatalf("state = %+v", s)
	}
	if !s.Steps["ra"].Adopted || h.count("Review it.") != 1 {
		t.Errorf("ra adopted %v, sent %d times", s.Steps["ra"].Adopted, h.count("Review it."))
	}
	if s.Steps["rb"].Adopted || h.count("Review it too.") != 2 {
		t.Errorf("rb adopted %v, sent %d times", s.Steps["rb"].Adopted, h.count("Review it too."))
	}
	if !slices.Contains(h.opened, "a="+recorded) {
		t.Errorf("session a not reopened as %s: %v", recorded, h.opened)
	}
	if err := r2.Resume(context.Background(), id); !errors.Is(err, workflow.ErrRunEnded) {
		t.Errorf("resuming an ended run: err = %v", err)
	}
	if !slices.Contains(store.kinds(id), workflow.KindRunResumed) {
		t.Errorf("log = %v", store.kinds(id))
	}
}

func TestShutdownLeavesRunsToResume(t *testing.T) {
	h := newFakeHarness(func(harness.Request) script { return script{gate: make(chan struct{})} })
	store := newLogStore()
	r := workflow.NewRunner(h.open, store, 0)
	id, err := r.Start(context.Background(), load(t, parallel))
	if err != nil {
		t.Fatal(err)
	}
	until(t, r, id, started("d"))
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kinds := store.kinds(id); slices.Contains(kinds, workflow.KindRunEnded) || slices.Contains(kinds, workflow.KindStepEnded) {
		t.Errorf("an interrupted run logged its end: %v", kinds)
	}
	if h.closed != h.conns {
		t.Errorf("closed %d of %d sessions", h.closed, h.conns)
	}
	if _, err := r.Start(context.Background(), load(t, parallel)); !errors.Is(err, workflow.ErrShutdown) {
		t.Errorf("Start after Shutdown: err = %v", err)
	}
}

func TestUsageLimitPauses(t *testing.T) {
	var mu sync.Mutex
	limited := false
	h := newFakeHarness(func(harness.Request) script {
		mu.Lock()
		defer mu.Unlock()
		if !limited {
			limited = true
			return script{err: &harness.LimitError{ResetsAt: time.Now().Add(100 * time.Millisecond)}}
		}
		return script{}
	})
	store := newLogStore()
	r := workflow.NewRunner(h.open, store, 0)
	w := workflow.Workflow{Name: "w", Sessions: []workflow.SessionSpec{{Name: "s"}}, Steps: []workflow.Step{{ID: "a", Session: "s", Prompt: "go"}}}
	id, err := r.Start(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if s := wait(t, r, id); s.Status != workflow.StatusDone {
		t.Fatalf("state = %+v", s)
	}
	want := []workflow.Kind{
		workflow.KindRunStarted, workflow.KindSessionOpened, workflow.KindStepStarted, workflow.KindRunPaused,
		workflow.KindRunResumed, workflow.KindStepStarted, workflow.KindStepEnded, workflow.KindRunEnded,
	}
	if got := store.kinds(id); !slices.Equal(got, want) {
		t.Errorf("log = %v, want %v", got, want)
	}
}

func TestSubscribe(t *testing.T) {
	gate := make(chan struct{})
	h := newFakeHarness(func(harness.Request) script { return script{gate: gate, structured: json.RawMessage(`{}`)} })
	r := workflow.NewRunner(h.open, newLogStore(), 0)
	id, err := r.Start(context.Background(), load(t, review))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := r.Subscribe(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	close(gate)
	var live, logged int
	last := 0
	for e := range events {
		if !e.Logged() {
			live++
			continue
		}
		if e.Seq != last+1 {
			t.Fatalf("event %d follows %d", e.Seq, last)
		}
		last = e.Seq
		logged++
	}
	if live == 0 || logged == 0 {
		t.Fatalf("%d live and %d logged events", live, logged)
	}
	// After the run, a subscription replays the log after its cursor and closes.
	replay, err := r.Subscribe(ctx, id, last-1)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []workflow.Kind
	for e := range replay {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Equal(kinds, []workflow.Kind{workflow.KindRunEnded}) {
		t.Errorf("replay = %v", kinds)
	}
	if _, err := r.Subscribe(ctx, "nope", 0); !errors.Is(err, workflow.ErrUnknownRun) {
		t.Errorf("unknown run: err = %v", err)
	}
}
