package workflow

import (
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// ErrLog is returned, wrapped, by Apply and Fold for an event that doesn't follow from the
// state it is applied to: out of sequence, before the run started, or naming a step or
// session the workflow doesn't declare.
var ErrLog = errors.New("workflow: inconsistent log")

// Status is where a run or a step stands.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusPaused    Status = "paused"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Ended reports whether s is final.
func (s Status) Ended() bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}

// State is a run as its log has it so far: the fold of its events.
type State struct {
	RunID    string   `json:"runId"`
	Workflow Workflow `json:"workflow"`
	Status   Status   `json:"status"`
	// Seq is the last logged event's.
	Seq int `json:"seq"`
	// Sessions maps each opened session of the workflow to its harness session ID.
	Sessions map[string]string    `json:"sessions"`
	Steps    map[string]StepState `json:"steps"`
	Started  time.Time            `json:"started"`
	Ended    time.Time            `json:"ended,omitzero"`
	// Until is when a paused run goes on.
	Until time.Time `json:"until,omitzero"`
	Err   string    `json:"err,omitempty"`
}

// StepState is where one step stands.
type StepState struct {
	Status     Status          `json:"status"`
	ExchangeID uuid.UUID       `json:"exchangeId,omitzero"`
	Result     *harness.Result `json:"result,omitempty"`
	Adopted    bool            `json:"adopted,omitempty"`
	Err        string          `json:"err,omitempty"`
	Started    time.Time       `json:"started,omitzero"`
	Ended      time.Time       `json:"ended,omitzero"`
}

// Fold applies events, a run's log in order, to an empty State.
func Fold(events []Event) (State, error) {
	var s State
	for _, e := range events {
		if err := s.Apply(e); err != nil {
			return s, err
		}
	}
	return s, nil
}

// Apply folds one logged event into s.
func (s *State) Apply(e Event) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: event %d (%s): "+format, append([]any{ErrLog, e.Seq, e.Kind}, args...)...)
	}
	if !e.Logged() {
		return fail("not a logged event")
	}
	if e.Seq != s.Seq+1 {
		return fail("follows event %d", s.Seq)
	}
	if e.Kind == KindRunStarted {
		if s.Seq != 0 {
			return fail("the run has already started")
		}
		if e.Workflow == nil {
			return fail("no workflow")
		}
		*s = State{
			RunID:    e.RunID,
			Workflow: *e.Workflow,
			Status:   StatusRunning,
			Sessions: map[string]string{},
			Steps:    map[string]StepState{},
			Started:  e.Time,
		}
		for _, st := range e.Workflow.Steps {
			s.Steps[st.ID] = StepState{Status: StatusPending}
		}
		s.Seq = e.Seq
		return nil
	}
	if s.Seq == 0 {
		return fail("before the run started")
	}
	if e.RunID != s.RunID {
		return fail("belongs to run %s", e.RunID)
	}
	if s.Status.Ended() {
		return fail("after the run ended")
	}
	switch e.Kind {
	case KindRunResumed:
		s.Status, s.Until = StatusRunning, time.Time{}
	case KindSessionOpened:
		if _, ok := s.Workflow.Session(e.Session); !ok {
			return fail("no session %q", e.Session)
		}
		s.Sessions[e.Session] = e.SessionID
	case KindStepStarted:
		if _, ok := s.Steps[e.Step]; !ok {
			return fail("no step %q", e.Step)
		}
		s.Steps[e.Step] = StepState{Status: StatusRunning, ExchangeID: e.ExchangeID, Started: e.Time}
	case KindStepEnded:
		st, ok := s.Steps[e.Step]
		if !ok {
			return fail("no step %q", e.Step)
		}
		if !e.Status.Ended() {
			return fail("step status %q", e.Status)
		}
		st.Status, st.Result, st.Adopted, st.Err, st.Ended = e.Status, e.Result, e.Adopted, e.Err, e.Time
		if e.ExchangeID != uuid.Nil() {
			st.ExchangeID = e.ExchangeID
		}
		s.Steps[e.Step] = st
	case KindRunPaused:
		s.Status, s.Until = StatusPaused, e.Until
	case KindRunEnded:
		if !e.Status.Ended() {
			return fail("run status %q", e.Status)
		}
		s.Status, s.Err, s.Ended = e.Status, e.Err, e.Time
	default:
		return fail("unknown kind")
	}
	s.Seq = e.Seq
	return nil
}

// Results returns the results of the steps that are done, by step ID.
func (s State) Results() map[string]harness.Result {
	results := map[string]harness.Result{}
	for id, st := range s.Steps {
		if st.Status == StatusDone && st.Result != nil {
			results[id] = *st.Result
		}
	}
	return results
}

// Done counts the steps that are done.
func (s State) Done() int {
	n := 0
	for _, st := range s.Steps {
		if st.Status == StatusDone {
			n++
		}
	}
	return n
}
