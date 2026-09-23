package pi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// codec is Pi's stdio.Codec.
type codec struct{}

var _ stdio.Codec = codec{}

// Encode renders a command with its correlation id. Pi echoes the id in the command's
// response, and runs commands concurrently, so responses may come back in any order.
func (codec) Encode(id string, cmd any) ([]byte, error) {
	c, ok := cmd.(command)
	if !ok {
		return nil, fmt.Errorf("pi: encode %T: not a command", cmd)
	}
	c.ID = id
	return json.Marshal(c)
}

// Decode turns a response line into a Response, and any other line into normalized events.
func (codec) Decode(line []byte) (stdio.Frame, error) {
	r, err := decode(line)
	if err != nil {
		return stdio.Frame{}, err
	}
	if r.Type == "response" {
		resp := &stdio.Response{ID: r.ID, Data: r.Data}
		if !r.Success {
			resp.Err = errors.New("pi: " + r.Command + ": " + r.Error)
		}
		return stdio.Frame{Response: resp}, nil
	}
	return stdio.Frame{Events: normalize(r, line)}, nil
}

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
// event, so nothing Pi reports is lost: events with no normalized meaning become EventHarness.
//
// Pi's events carry no request ID. An exchange is the window from agent_start to
// agent_settled, the one signal that no retry, compaction, or queued message will start
// another run, so agent_settled maps to EventEnded.
func normalize(r record, raw []byte) []harness.Event {
	ev := harness.Event{Kind: harness.EventHarness, Raw: raw}
	switch r.Type {
	case "agent_start":
		ev.Kind = harness.EventStarted
	case "agent_settled":
		ev.Kind = harness.EventEnded
	case "message_update":
		if r.AssistantMessageEvent == nil {
			break
		}
		switch r.AssistantMessageEvent.Type {
		case "text_delta":
			ev.Kind, ev.Text = harness.EventTextDelta, r.AssistantMessageEvent.Delta
		case "thinking_delta":
			ev.Kind, ev.Text = harness.EventThinkingDelta, r.AssistantMessageEvent.Delta
		}
	case "tool_execution_start":
		ev.Kind = harness.EventToolCall
		ev.Tool = &harness.ToolEvent{CallID: r.ToolCallID, Name: r.ToolName, Args: r.Args}
	case "tool_execution_end":
		ev.Kind = harness.EventToolResult
		ev.Tool = &harness.ToolEvent{
			CallID: r.ToolCallID, Name: r.ToolName, Result: r.Result, IsError: r.IsError,
		}
	case "extension_error":
		ev.Kind, ev.Err = harness.EventError, string(raw)
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
// EventCancelled, and an errored one by EventError.
func messageEnd(m *message, raw []byte) []harness.Event {
	ev := harness.Event{
		Kind:       harness.EventMessageEnd,
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
		events = append(events, harness.Event{Kind: harness.EventCancelled, StopReason: m.StopReason})
	case "error":
		events = append(events, harness.Event{Kind: harness.EventError, Err: m.ErrorMessage})
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
