package pi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// decode parses one stdout line.
func decode(line []byte) (record, error) {
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return r, fmt.Errorf("pi: decode %q: %w", line, err)
	}
	return r, nil
}

// normalize maps one Pi event to normalized events, without session or exchange IDs or
// sequence numbers, which the session stamps. Every event maps to at least one normalized
// event, so nothing Pi reports is lost: events with no normalized meaning become KindHarness.
//
// Pi's events carry no request ID. An exchange is the window from agent_start to
// agent_settled, the one signal that no retry, compaction, or queued message will start
// another run, so agent_settled maps to KindEnded.
func normalize(r record, raw []byte) []harness.Event {
	ev := harness.Event{Kind: harness.KindHarness, Raw: raw}
	switch r.Type {
	case "agent_start":
		ev.Kind = harness.KindStarted
	case "agent_settled":
		ev.Kind = harness.KindEnded
	case "message_update":
		if r.AssistantMessageEvent == nil {
			break
		}
		switch r.AssistantMessageEvent.Type {
		case "text_delta":
			ev.Kind, ev.Text = harness.KindTextDelta, r.AssistantMessageEvent.Delta
		case "thinking_delta":
			ev.Kind, ev.Text = harness.KindThinkingDelta, r.AssistantMessageEvent.Delta
		}
	case "tool_execution_start":
		ev.Kind = harness.KindToolCall
		ev.Tool = &harness.ToolEvent{CallID: r.ToolCallID, Name: r.ToolName, Args: r.Args}
	case "tool_execution_end":
		ev.Kind = harness.KindToolResult
		ev.Tool = &harness.ToolEvent{
			CallID: r.ToolCallID, Name: r.ToolName, Result: r.Result, IsError: r.IsError,
		}
	case "extension_error":
		ev.Kind, ev.Err = harness.KindError, string(raw)
	case "message_end":
		// Pi also ends system and user messages; only the assistant's carries a stop reason.
		if r.Message == nil || r.Message.Role != "assistant" {
			break
		}
		return messageEnd(r.Message, raw)
	}
	return []harness.Event{ev}
}

// messageEnd normalizes an assistant message_end. An aborted message is followed by
// KindCancelled, and an errored one by KindError.
func messageEnd(m *message, raw []byte) []harness.Event {
	ev := harness.Event{
		Kind:       harness.KindMessageEnd,
		Text:       messageText(m.Content),
		StopReason: m.StopReason,
		Raw:        raw,
	}
	if m.Usage != nil {
		ev.Usage = &harness.Usage{Input: m.Usage.Input, Output: m.Usage.Output}
	}
	events := []harness.Event{ev}
	switch m.StopReason {
	case "aborted":
		events = append(events, harness.Event{Kind: harness.KindCancelled, StopReason: m.StopReason})
	case "error":
		events = append(events, harness.Event{Kind: harness.KindError, Err: m.ErrorMessage})
	}
	return events
}

// messageText concatenates the text blocks of an assistant message's content.
func messageText(content json.RawMessage) string {
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range blocks {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}
