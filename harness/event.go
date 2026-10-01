package harness

import (
	"encoding/json"
	"time"
	"uuid"
)

// EventKind classifies a normalized event.
type EventKind string

const (
	// EventStarted marks the harness starting work on the exchange.
	EventStarted EventKind = "started"
	// EventTextDelta carries a fragment of assistant text in Text.
	EventTextDelta EventKind = "text_delta"
	// EventThinkingDelta carries a fragment of the model's reasoning in Text.
	EventThinkingDelta EventKind = "thinking_delta"
	// EventToolCall reports that a tool began executing, in Tool.
	EventToolCall EventKind = "tool_call"
	// EventToolResult reports a tool's result, in Tool.
	EventToolResult EventKind = "tool_result"
	// EventMessageEnd closes one assistant message, with its StopReason and Text.
	EventMessageEnd EventKind = "message_end"
	// EventCancelled reports that the exchange was cancelled.
	EventCancelled EventKind = "cancelled"
	// EventStructured carries the exchange's structured response in Structured: the value
	// the harness validated against the request's schema.
	EventStructured EventKind = "structured"
	// EventStructuredRejected reports a structured response that failed validation against
	// the request's schema, with the failure in Err. The run goes on, and the model may try
	// again; it doesn't end the exchange or become the exchange's error.
	EventStructuredRejected EventKind = "structured_rejected"
	// EventLimit carries the harness's notice of the provider's usage or rate limit in Limit,
	// without ending the run. A run the limit refuses or cuts off reports an EventError with
	// a *LimitError instead.
	EventLimit EventKind = "limit"
	// EventError carries a harness or process error in Err.
	EventError EventKind = "error"
	// EventEnded is the last event of every exchange.
	EventEnded EventKind = "ended"
	// EventHarness carries a harness event with no normalized meaning, in Raw only.
	EventHarness EventKind = "harness"
)

// Event is one normalized event of an exchange. An adapter's Connection fills in everything but
// SessionID, ExchangeID, and Seq, which the session stamps.
type Event struct {
	SessionID  string
	ExchangeID uuid.UUID
	// Seq numbers the exchange's events from 1.
	Seq  int
	Kind EventKind
	// Text holds a delta for the delta kinds, and the message text for EventMessageEnd.
	Text string
	Tool *ToolEvent
	// StopReason is set on EventMessageEnd, EventCancelled, and EventEnded.
	StopReason string
	// Usage is set on EventMessageEnd when the harness reports it. An adapter reports usage
	// once, per message or per turn, so no tokens are counted twice, and the session sums the
	// reports into the exchange's Result.
	Usage *Usage
	// Structured is set on EventStructured.
	Structured json.RawMessage
	// Limit is set on EventLimit.
	Limit *LimitStatus
	// Err is set on EventError and EventStructuredRejected. It is the error itself, so callers can
	// match it with errors.Is, and an Event therefore doesn't serialize directly.
	Err error
	// Raw is the harness's own record the event came from, when there is one.
	Raw json.RawMessage
}

// ToolEvent describes a tool execution.
type ToolEvent struct {
	CallID  string
	Name    string
	Args    json.RawMessage
	Result  json.RawMessage
	IsError bool
}

// LimitStatus is a harness's notice of where a session stands against the provider's usage or
// rate limit.
type LimitStatus struct {
	// Status is the harness's own word for it, such as "allowed", "allowed_warning", or
	// "rejected".
	Status string
	// Kind names the limit, such as a five-hour or a weekly window, when the harness says.
	Kind string
	// Utilization is the fraction of the limit used, from 0 to 1, when the harness says.
	Utilization float64
	// ResetsAt is when the limit resets, and zero when the harness doesn't say.
	ResetsAt time.Time
}
