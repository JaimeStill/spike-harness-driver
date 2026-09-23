package harness

import "encoding/json"

// Kind classifies a normalized event.
type Kind string

const (
	// KindStarted marks the harness starting work on the exchange.
	KindStarted Kind = "started"
	// KindTextDelta carries a fragment of assistant text in Text.
	KindTextDelta Kind = "text_delta"
	// KindThinkingDelta carries a fragment of the model's reasoning in Text.
	KindThinkingDelta Kind = "thinking_delta"
	// KindToolCall reports that a tool began executing, in Tool.
	KindToolCall Kind = "tool_call"
	// KindToolResult reports a tool's result, in Tool.
	KindToolResult Kind = "tool_result"
	// KindMessageEnd closes one assistant message, with its StopReason.
	KindMessageEnd Kind = "message_end"
	// KindCancelled reports that the exchange was cancelled.
	KindCancelled Kind = "cancelled"
	// KindError carries a harness or process error in Err.
	KindError Kind = "error"
	// KindEnded is the last event of every exchange.
	KindEnded Kind = "ended"
	// KindHarness carries a harness event with no normalized meaning, in Raw only.
	KindHarness Kind = "harness"
)

// Event is one normalized event of an exchange.
type Event struct {
	SessionID  string
	ExchangeID string
	// Seq numbers the exchange's events from 1.
	Seq  int
	Kind Kind
	// Text holds a delta for the delta kinds.
	Text string
	Tool *ToolEvent
	// StopReason is set on KindMessageEnd and KindEnded.
	StopReason string
	// Usage is set on KindMessageEnd when the harness reports it.
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
