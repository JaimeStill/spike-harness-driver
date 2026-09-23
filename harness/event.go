package harness

import (
	"encoding/json"
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
	// EventError carries a harness or process error in Err.
	EventError EventKind = "error"
	// EventEnded is the last event of every exchange.
	EventEnded EventKind = "ended"
	// EventHarness carries a harness event with no normalized meaning, in Raw only.
	EventHarness EventKind = "harness"
)

// Event is one normalized event of an exchange. An adapter's Conn fills in everything but
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
	// Usage is set on EventMessageEnd when the harness reports it.
	Usage *Usage
	Err   string
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
