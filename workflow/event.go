package workflow

import (
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Kind classifies an Event.
type Kind string

const (
	// KindRunStarted is a run's first event, carrying its Workflow.
	KindRunStarted Kind = "run_started"
	// KindRunResumed marks a runner taking up a run its log left unfinished, as after a
	// restart, or a paused run going on.
	KindRunResumed Kind = "run_resumed"
	// KindSessionOpened binds one of the workflow's sessions, by Session, to the harness
	// session it runs on, by SessionID. A resume reopens the session by that ID.
	KindSessionOpened Kind = "session_opened"
	// KindStepStarted reports that the harness accepted a step's prompt, with the exchange's
	// ExchangeID and the rendered Prompt.
	KindStepStarted Kind = "step_started"
	// KindStepEnded reports how a step ended, in Status: done with its Result, failed with Err,
	// or cancelled. Adopted marks a step a resume took from the harness session's records,
	// because its exchange ended after the runner last logged it.
	KindStepEnded Kind = "step_ended"
	// KindRunPaused reports that the run waits until Until, as for a provider's usage limit.
	KindRunPaused Kind = "run_paused"
	// KindRunEnded is a run's last event, with its Status and, for a failed run, Err.
	KindRunEnded Kind = "run_ended"
	// KindExchange carries one event of a step's exchange in Exchange. It reaches subscribers
	// live and is never logged, so it has no Seq.
	KindExchange Kind = "exchange"
)

// Event is one event of a run. Every kind but KindExchange is logged, numbered by Seq from 1,
// and folds into the run's State.
type Event struct {
	RunID string `json:"runId"`
	// Seq numbers the run's logged events from 1, and is zero for a live KindExchange event.
	Seq  int       `json:"seq,omitzero"`
	Kind Kind      `json:"kind"`
	Time time.Time `json:"time"`
	// Workflow is set on KindRunStarted.
	Workflow *Workflow `json:"workflow,omitempty"`
	// Session names the workflow's session, and SessionID the harness's.
	Session   string `json:"session,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Step      string `json:"step,omitempty"`
	// ExchangeID is the step's exchange, on KindStepStarted, KindStepEnded, and KindExchange.
	ExchangeID uuid.UUID `json:"exchangeId,omitzero"`
	Prompt     string    `json:"prompt,omitempty"`
	// Status is set on KindStepEnded and KindRunEnded.
	Status  Status          `json:"status,omitempty"`
	Result  *harness.Result `json:"result,omitempty"`
	Adopted bool            `json:"adopted,omitempty"`
	Err     string          `json:"err,omitempty"`
	Until   time.Time       `json:"until,omitzero"`
	// Exchange is set on KindExchange.
	Exchange *ExchangeEvent `json:"exchange,omitempty"`
}

// Logged reports whether e belongs in the run's log.
func (e Event) Logged() bool { return e.Kind != KindExchange }

// ExchangeEvent is what a subscriber sees of one harness event: harness.Event without its raw
// record, and with its error as text, so it serializes.
type ExchangeEvent struct {
	Kind harness.EventKind `json:"kind"`
	Text string            `json:"text,omitempty"`
	// Tool names the tool of a tool call or result.
	Tool string `json:"tool,omitempty"`
	Err  string `json:"err,omitempty"`
}

// exchangeEvent converts a harness event.
func exchangeEvent(ev harness.Event) *ExchangeEvent {
	x := &ExchangeEvent{Kind: ev.Kind, Text: ev.Text}
	if ev.Tool != nil {
		x.Tool = ev.Tool.Name
	}
	if ev.Err != nil {
		x.Err = ev.Err.Error()
	}
	return x
}
