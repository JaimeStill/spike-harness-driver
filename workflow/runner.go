package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

var (
	// ErrUnknownRun is returned for a run ID the Store holds no log of.
	ErrUnknownRun = errors.New("workflow: unknown run")
	// ErrRunActive is returned by Resume for a run the Runner is already running.
	ErrRunActive = errors.New("workflow: run is active")
	// ErrRunEnded is returned by Resume and Cancel for a run whose log has ended.
	ErrRunEnded = errors.New("workflow: run has ended")
	// ErrCancelled is the cause of a run's context once Cancel ends it.
	ErrCancelled = errors.New("workflow: run cancelled")
	// ErrShutdown is the cause of a run's context once Shutdown interrupts it. An interrupted
	// run logs no end, so a runner that starts later resumes it.
	ErrShutdown = errors.New("workflow: runner shut down")
)

// Opener opens one of a workflow's sessions. sessionID is the harness session ID a resumed run
// recorded for it, and empty for a session the run hasn't opened before. The host decides what
// spec's harness, provider, and model mean, and gives a session the same working directory each
// time, since a harness such as Pi scopes its session IDs to it. ctx bounds only the setup.
type Opener func(ctx context.Context, spec SessionSpec, sessionID string) (*harness.Session, error)

// Store keeps each run's log.
type Store interface {
	// Append adds e to its run's log. The Runner numbers a run's events itself and appends them
	// one at a time, in order. Append should fail for an event whose Seq doesn't follow the
	// log's last, which is how a second process driving the same run is caught.
	Append(ctx context.Context, e Event) error
	// Events returns the run's logged events with a Seq above after, in order, and none for a
	// run it has no log of.
	Events(ctx context.Context, runID string, after int) ([]Event, error)
	// Runs returns the IDs of the runs it holds logs of.
	Runs(ctx context.Context) ([]string, error)
}

const limitRetry = time.Minute

const logTimeout = 30 * time.Second

// Runner runs workflows. Each run has a goroutine of its own, and its steps run concurrently
// within the Runner's limit on exchanges in flight, which every run shares.
type Runner struct {
	open  Opener
	store Store
	slots chan struct{}

	ctx      context.Context
	shutdown context.CancelCauseFunc
	wg       sync.WaitGroup

	mu     sync.Mutex
	runs   map[string]*run
	closed bool
}

// NewRunner returns a Runner that opens sessions with open and logs runs in store. limit bounds
// the exchanges in flight across every run; zero or less means no limit.
func NewRunner(open Opener, store Store, limit int) *Runner {
	r := &Runner{open: open, store: store, runs: map[string]*run{}}
	if limit > 0 {
		r.slots = make(chan struct{}, limit)
	}
	r.ctx, r.shutdown = context.WithCancelCause(context.Background())
	return r
}

// Start validates w, logs a new run of it, and runs it in the background. It returns the run's
// ID, which sorts by creation time.
func (r *Runner) Start(ctx context.Context, w Workflow) (string, error) {
	if err := w.Validate(); err != nil {
		return "", err
	}
	rn, err := r.newRun(uuid.NewV7().String(), State{})
	if err != nil {
		return "", err
	}
	if err := rn.log(Event{Kind: KindRunStarted, Workflow: &w}); err != nil {
		r.drop(rn)
		return "", err
	}
	r.launch(rn)
	return rn.id, nil
}

// Resume takes up a run whose log hasn't ended, as after a restart. Its finished steps keep
// their results. Each session reopens under the harness session ID the log recorded, and a step
// that started but has no end is adopted from the session's exchange records when its exchange
// ended, and runs again otherwise. A paused run stays paused until its log's Until.
//
// A log that records a failed or cancelled step but no end, as when a Shutdown or a crash
// landed while the run was ending, ends the same way once its steps in flight are settled: the
// outcome was decided before the runner stopped.
func (r *Runner) Resume(ctx context.Context, id string) error {
	r.mu.Lock()
	_, active := r.runs[id]
	r.mu.Unlock()
	if active {
		return fmt.Errorf("%w: %s", ErrRunActive, id)
	}
	s, err := r.fold(ctx, id)
	if err != nil {
		return err
	}
	if s.Status.Ended() {
		return fmt.Errorf("%w: %s", ErrRunEnded, id)
	}
	rn, err := r.newRun(id, s)
	if err != nil {
		return err
	}
	rn.settle()
	if s.Status != StatusPaused {
		if err := rn.log(Event{Kind: KindRunResumed}); err != nil {
			r.drop(rn)
			return err
		}
	}
	r.launch(rn)
	return nil
}

// ResumeAll resumes every run the Store holds whose log hasn't ended and that the Runner isn't
// running, and returns their IDs.
func (r *Runner) ResumeAll(ctx context.Context) ([]string, error) {
	ids, err := r.store.Runs(ctx)
	if err != nil {
		return nil, err
	}
	var resumed []string
	var errs []error
	for _, id := range ids {
		err := r.Resume(ctx, id)
		switch {
		case err == nil:
			resumed = append(resumed, id)
		case errors.Is(err, ErrRunEnded), errors.Is(err, ErrRunActive):
		default:
			errs = append(errs, err)
		}
	}
	return resumed, errors.Join(errs...)
}

// Cancel ends a run as cancelled: its exchanges in flight are cancelled, and the run's log ends
// once they have. A run whose log hasn't ended but which no runner is running ends at once.
func (r *Runner) Cancel(ctx context.Context, id string) error {
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn != nil {
		rn.cancel(ErrCancelled)
		if cause := context.Cause(rn.ctx); !errors.Is(cause, ErrCancelled) {
			return cause
		}
		return nil
	}
	s, err := r.fold(ctx, id)
	if err != nil {
		return err
	}
	if s.Status.Ended() {
		return fmt.Errorf("%w: %s", ErrRunEnded, id)
	}
	e := Event{RunID: id, Seq: s.Seq + 1, Kind: KindRunEnded, Time: time.Now(), Status: StatusCancelled}
	return r.store.Append(ctx, e)
}

// State returns the run's state as its log has it.
func (r *Runner) State(ctx context.Context, id string) (State, error) {
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn != nil {
		rn.mu.Lock()
		defer rn.mu.Unlock()
		return rn.state, nil
	}
	return r.fold(ctx, id)
}

// Runs returns the state of every run the Store holds, oldest first. A run whose log can't be
// read or folded is left out, and the error names it, so one bad log doesn't hide the others.
func (r *Runner) Runs(ctx context.Context) ([]State, error) {
	ids, err := r.store.Runs(ctx)
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	states := make([]State, 0, len(ids))
	var errs []error
	for _, id := range ids {
		s, err := r.State(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("run %s: %w", id, err))
			continue
		}
		states = append(states, s)
	}
	return states, errors.Join(errs...)
}

// Wait blocks until the Runner stops running the run, and returns its state then: ended, or
// interrupted by Shutdown. For a run it isn't running, it returns the state at once.
func (r *Runner) Wait(ctx context.Context, id string) (State, error) {
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn != nil {
		select {
		case <-rn.done:
		case <-ctx.Done():
			return State{}, ctx.Err()
		}
		rn.mu.Lock()
		defer rn.mu.Unlock()
		return rn.state, rn.interruption
	}
	return r.fold(ctx, id)
}

// Subscribe returns a channel of the run's events with a Seq above after: the logged events
// first, then, while the Runner runs the run, its events as they happen, including its
// exchanges' live events. The channel closes after the run's last event, when the Runner stops
// running the run, or when ctx is done.
func (r *Runner) Subscribe(ctx context.Context, id string, after int) (<-chan Event, error) {
	r.mu.Lock()
	rn := r.runs[id]
	r.mu.Unlock()
	if rn != nil {
		rn.mu.Lock()
		defer rn.mu.Unlock()
		if !rn.finished {
			events, err := r.store.Events(ctx, id, after)
			if err != nil {
				return nil, err
			}
			q := newQueue(ctx)
			q.push(events...)
			rn.subs = append(rn.subs, q)
			return q.out, nil
		}
	}
	events, err := r.store.Events(ctx, id, after)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		if _, err := r.fold(ctx, id); err != nil {
			return nil, err
		}
	}
	q := newQueue(ctx)
	q.push(events...)
	q.close()
	return q.out, nil
}

// Shutdown interrupts every run, without logging an end for any, so a Runner that starts later
// resumes them, and waits until their goroutines have closed their sessions or ctx is done. The
// Runner starts nothing afterwards.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.shutdown(ErrShutdown)
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) fold(ctx context.Context, id string) (State, error) {
	events, err := r.store.Events(ctx, id, 0)
	if err != nil {
		return State{}, err
	}
	if len(events) == 0 {
		return State{}, fmt.Errorf("%w: %s", ErrUnknownRun, id)
	}
	return Fold(events)
}

func (r *Runner) tryAcquire() bool {
	if r.slots == nil {
		return true
	}
	select {
	case r.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *Runner) release() {
	if r.slots == nil {
		return
	}
	<-r.slots
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rn := range r.runs {
		rn.signal()
	}
}

func (r *Runner) newRun(id string, s State) (*run, error) {
	ctx, cancel := context.WithCancelCause(r.ctx)
	steps, stop := context.WithCancel(ctx)
	rn := &run{
		r: r, id: id, ctx: ctx, cancel: cancel, steps: steps, stop: stop,
		state:    s,
		sessions: map[string]*harness.Session{},
		inflight: map[string]bool{},
		busy:     map[string]bool{},
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		cancel(nil)
		return nil, ErrShutdown
	case r.runs[id] != nil:
		cancel(nil)
		return nil, fmt.Errorf("%w: %s", ErrRunActive, id)
	}
	r.runs[id] = rn
	r.wg.Add(1)
	return rn, nil
}

func (r *Runner) launch(rn *run) {
	go func() {
		defer r.wg.Done()
		rn.loop()
	}()
}

func (r *Runner) drop(rn *run) {
	rn.cancel(nil)
	rn.mu.Lock()
	rn.finished = true
	for _, q := range rn.subs {
		q.close()
	}
	rn.subs = nil
	rn.mu.Unlock()
	r.mu.Lock()
	delete(r.runs, rn.id)
	r.mu.Unlock()
	close(rn.done)
	r.wg.Done()
}

type run struct {
	r      *Runner
	id     string
	ctx    context.Context
	cancel context.CancelCauseFunc
	steps  context.Context
	stop   context.CancelFunc

	mu           sync.Mutex
	state        State
	subs         []*queue
	sessions     map[string]*harness.Session
	busy         map[string]bool
	inflight     map[string]bool
	failure      error
	interruption error
	finished     bool

	wake chan struct{}
	done chan struct{}
}

func (rn *run) log(e Event) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	e.RunID, e.Seq, e.Time = rn.id, rn.state.Seq+1, time.Now()
	next := rn.state
	if next.Seq > 0 {
		next.Sessions = cloneMap(next.Sessions)
		next.Steps = cloneMap(next.Steps)
	}
	if err := next.Apply(e); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), logTimeout)
	defer cancel()
	if err := rn.r.store.Append(ctx, e); err != nil {
		err = fmt.Errorf("workflow: log run %s: %w", rn.id, err)
		rn.cancel(err)
		return err
	}
	rn.state = next
	rn.subs = slices.DeleteFunc(rn.subs, (*queue).gone)
	for _, q := range rn.subs {
		q.push(e)
	}
	return nil
}

func cloneMap[V any](m map[string]V) map[string]V {
	c := make(map[string]V, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (rn *run) live(e Event) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	e.RunID, e.Time = rn.id, time.Now()
	for _, q := range rn.subs {
		q.push(e)
	}
}

func (rn *run) interrupted() bool {
	return rn.ctx.Err() != nil && !errors.Is(context.Cause(rn.ctx), ErrCancelled)
}

func (rn *run) settle() {
	for _, st := range rn.state.Workflow.Steps {
		switch s := rn.state.Steps[st.ID]; s.Status {
		case StatusFailed:
			if rn.failure == nil {
				rn.failure = fmt.Errorf("step %q: %s", st.ID, s.Err)
			}
		case StatusCancelled:
			rn.cancel(ErrCancelled)
		}
	}
	if rn.failure != nil {
		rn.stop()
	}
}

func (rn *run) signal() {
	select {
	case rn.wake <- struct{}{}:
	default:
	}
}

func (rn *run) loop() {
	defer rn.finish()
	var timer *time.Timer
	for {
		var wait <-chan time.Time
		done := rn.ctx.Done()
		rn.mu.Lock()
		stopping := rn.ctx.Err() != nil || rn.failure != nil
		if stopping {
			done = nil
			if len(rn.inflight) == 0 {
				rn.mu.Unlock()
				break
			}
		} else if rn.state.Done() == len(rn.state.Workflow.Steps) {
			rn.mu.Unlock()
			_ = rn.log(Event{Kind: KindRunEnded, Status: StatusDone})
			return
		} else if rn.state.Status == StatusPaused {
			if d := time.Until(rn.state.Until); d > 0 {
				timer = resetTimer(timer, d)
				wait = timer.C
			} else {
				rn.mu.Unlock()
				_ = rn.log(Event{Kind: KindRunResumed})
				continue
			}
		} else {
			ready := rn.ready()
			if len(ready) == 0 && len(rn.inflight) == 0 {
				rn.failure = errors.New("workflow: no step can start")
				rn.mu.Unlock()
				continue
			}
			for _, st := range ready {
				if !rn.r.tryAcquire() {
					break
				}
				rn.inflight[st.ID], rn.busy[st.Session] = true, true
				go rn.step(st)
			}
		}
		rn.mu.Unlock()
		select {
		case <-rn.wake:
		case <-done:
		case <-wait:
		}
	}
	rn.mu.Lock()
	failure := rn.failure
	rn.mu.Unlock()
	switch {
	case failure != nil:
		_ = rn.log(Event{Kind: KindRunEnded, Status: StatusFailed, Err: failure.Error()})
	case rn.interrupted():
	default:
		_ = rn.log(Event{Kind: KindRunEnded, Status: StatusCancelled})
	}
}

func resetTimer(t *time.Timer, d time.Duration) *time.Timer {
	if t == nil {
		return time.NewTimer(d)
	}
	t.Reset(d)
	return t
}

func (rn *run) ready() []Step {
	var steps []Step
	busy := cloneMap(rn.busy)
	for _, st := range rn.state.Workflow.Steps {
		s := rn.state.Steps[st.ID]
		if s.Status != StatusPending && s.Status != StatusRunning || rn.inflight[st.ID] || busy[st.Session] {
			continue
		}
		if !slices.ContainsFunc(st.After, func(dep string) bool { return rn.state.Steps[dep].Status != StatusDone }) {
			busy[st.Session] = true
			steps = append(steps, st)
		}
	}
	return steps
}

func (rn *run) finish() {
	rn.stop()
	rn.mu.Lock()
	sessions := rn.sessions
	rn.sessions = nil
	rn.finished = true
	if rn.interrupted() {
		rn.interruption = context.Cause(rn.ctx)
	}
	for _, q := range rn.subs {
		q.close()
	}
	rn.subs = nil
	rn.mu.Unlock()
	for _, sess := range sessions {
		_ = sess.Close()
	}
	rn.cancel(nil)
	rn.r.mu.Lock()
	delete(rn.r.runs, rn.id)
	rn.r.mu.Unlock()
	close(rn.done)
}

func (rn *run) step(st Step) {
	ctx := rn.steps
	defer rn.release(st)
	defer rn.r.release()
	sess, err := rn.session(ctx, st.Session)
	if err != nil {
		rn.end(ctx, st, uuid.Nil(), nil, err)
		return
	}
	rn.mu.Lock()
	w, prior, results := rn.state.Workflow, rn.state.Steps[st.ID], rn.state.Results()
	rn.mu.Unlock()
	if prior.Status == StatusRunning && prior.ExchangeID != uuid.Nil() {
		adopted, err := rn.adopt(ctx, sess, st, prior.ExchangeID)
		if err != nil || adopted {
			if err != nil {
				rn.end(ctx, st, prior.ExchangeID, nil, err)
			}
			return
		}
	}
	prompt, err := w.Prompt(st.ID, results)
	if err != nil {
		rn.end(ctx, st, uuid.Nil(), nil, err)
		return
	}
	x, err := sess.Send(ctx, harness.Request{Text: prompt, Schema: st.Schema})
	if err != nil {
		rn.end(ctx, st, uuid.Nil(), nil, err)
		return
	}
	if err := rn.log(Event{Kind: KindStepStarted, Step: st.ID, Session: st.Session, ExchangeID: x.ID(), Prompt: prompt}); err != nil {
		x.Cancel()
	}
	for ev := range x.Events() {
		rn.live(Event{Kind: KindExchange, Step: st.ID, Session: st.Session, ExchangeID: x.ID(), Exchange: exchangeEvent(ev)})
	}
	res, err := x.Wait()
	rn.end(ctx, st, x.ID(), &res, err)
}

func (rn *run) adopt(ctx context.Context, sess *harness.Session, st Step, id uuid.UUID) (bool, error) {
	recs, err := sess.Exchanges(ctx)
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(recs, func(rec harness.Record) bool { return rec.ExchangeID == id })
	if i < 0 || recs[i].Err != "" || recs[i].Result.StopReason == "" || recs[i].Result.StopReason == "aborted" {
		return false, nil
	}
	res := recs[i].Result
	err = rn.log(Event{Kind: KindStepEnded, Step: st.ID, Session: st.Session, ExchangeID: id, Status: StatusDone, Result: &res, Adopted: true})
	return err == nil, err
}

func (rn *run) session(ctx context.Context, name string) (*harness.Session, error) {
	rn.mu.Lock()
	sess, recorded := rn.sessions[name], rn.state.Sessions[name]
	spec, _ := rn.state.Workflow.Session(name)
	rn.mu.Unlock()
	if sess != nil {
		return sess, nil
	}
	sess, err := rn.r.open(ctx, spec, recorded)
	if err != nil {
		return nil, fmt.Errorf("open session %q: %w", name, err)
	}
	rn.mu.Lock()
	rn.sessions[name] = sess
	rn.mu.Unlock()
	if sess.ID() != recorded {
		if err := rn.log(Event{Kind: KindSessionOpened, Session: name, SessionID: sess.ID()}); err != nil {
			return nil, err
		}
	}
	return sess, nil
}

func (rn *run) end(ctx context.Context, st Step, id uuid.UUID, res *harness.Result, err error) {
	e := Event{Kind: KindStepEnded, Step: st.ID, Session: st.Session, ExchangeID: id, Result: res}
	var limit *harness.LimitError
	switch {
	case rn.interrupted():
		return
	case ctx.Err() != nil:
		e.Status = StatusCancelled
	case errors.As(err, &limit):
		until := limit.ResetsAt
		if until.IsZero() {
			until = time.Now().Add(limitRetry)
		}
		rn.mu.Lock()
		later := rn.state.Status != StatusPaused || until.After(rn.state.Until)
		rn.mu.Unlock()
		if later {
			_ = rn.log(Event{Kind: KindRunPaused, Step: st.ID, Until: until, Err: err.Error()})
		}
		return
	case err != nil:
		e.Status, e.Err = StatusFailed, err.Error()
	default:
		e.Status = StatusDone
	}
	if lerr := rn.log(e); lerr != nil || e.Status != StatusFailed {
		return
	}
	rn.mu.Lock()
	if rn.failure == nil {
		rn.failure = fmt.Errorf("step %q: %w", st.ID, err)
	}
	rn.mu.Unlock()
	rn.stop()
}

func (rn *run) release(st Step) {
	rn.mu.Lock()
	var idle *harness.Session
	if !slices.ContainsFunc(rn.state.Workflow.Steps, func(s Step) bool {
		return s.Session == st.Session && rn.state.Steps[s.ID].Status != StatusDone
	}) {
		idle = rn.sessions[st.Session]
		delete(rn.sessions, st.Session)
	}
	rn.mu.Unlock()
	if idle != nil {
		_ = idle.Close()
	}
	rn.mu.Lock()
	delete(rn.inflight, st.ID)
	delete(rn.busy, st.Session)
	rn.mu.Unlock()
	rn.signal()
}
