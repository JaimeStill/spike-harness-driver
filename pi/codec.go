package pi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

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

// Decode turns a response line into a Response, a dialog into a Request, and any other line
// into normalized events.
func (codec) Decode(line []byte) (stdio.Frame, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return stdio.Frame{}, fmt.Errorf("pi: decode %q: %w", line, err)
	}
	if head.Type == "extension_ui_request" {
		return decodeUIRequest(line)
	}
	r, err := decode(line)
	if err != nil {
		return stdio.Frame{}, err
	}
	if r.Type == "response" {
		resp := &stdio.Response{ID: r.ID, Data: r.Data}
		if !r.Success {
			resp.Err = &commandError{Command: r.Command, Message: r.Error}
		}
		return stdio.Frame{Response: resp}, nil
	}
	return stdio.Frame{Events: normalize(r, line)}, nil
}

func decodeUIRequest(line []byte) (stdio.Frame, error) {
	var u uiRequest
	if err := json.Unmarshal(line, &u); err != nil {
		return stdio.Frame{}, fmt.Errorf("pi: decode %q: %w", line, err)
	}
	f := stdio.Frame{Events: []harness.Event{{Kind: harness.EventHarness, Raw: line}}}
	if dialogs[u.Method] {
		f.Request = &stdio.Request{ID: u.ID, Body: dialog{Method: u.Method, Title: u.Title, Placeholder: u.Placeholder}}
	}
	return f, nil
}

// Reply renders the answer to a dialog as an extension_ui_response. answer is the dialog's
// value, or nil to dismiss the dialog.
func (codec) Reply(req stdio.Request, answer any) ([]byte, error) {
	a := dialogAnswer{Type: "extension_ui_response", ID: req.ID}
	switch v := answer.(type) {
	case nil:
		a.Cancelled = true
	case string:
		a.Value = &v
	default:
		return nil, fmt.Errorf("pi: reply to %s: %T isn't a dialog answer", req.ID, answer)
	}
	return json.Marshal(a)
}

type commandError struct {
	Command string
	Message string
}

func (e *commandError) Error() string { return "pi: " + e.Command + ": " + e.Message }

func decode(line []byte) (record, error) {
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return r, fmt.Errorf("pi: decode %q: %w", line, err)
	}
	return r, nil
}

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
		if r.ToolName == respondTool {
			break
		}
		ev.Kind = harness.EventToolCall
		ev.Tool = &harness.ToolEvent{CallID: r.ToolCallID, Name: r.ToolName, Args: r.Args}
	case "tool_execution_end":
		if r.ToolName == respondTool {
			return []harness.Event{structured(r.Result, r.IsError, raw)}
		}
		ev.Kind = harness.EventToolResult
		ev.Tool = &harness.ToolEvent{
			CallID: r.ToolCallID, Name: r.ToolName, Result: r.Result, IsError: r.IsError,
		}
	case "extension_error":
		ev.Kind, ev.Err = harness.EventError, extensionError(raw)
	case "message_end":
		if r.Message == nil || r.Message.Role != "assistant" {
			break
		}
		return messageEnd(r.Message, raw)
	}
	return []harness.Event{ev}
}

func messageEnd(m *message, raw []byte) []harness.Event {
	ev := harness.Event{
		Kind:       harness.EventMessageEnd,
		Text:       messageText(m.Content),
		StopReason: m.StopReason,
		Raw:        raw,
	}
	if u := m.Usage; u != nil {
		ev.Usage = &harness.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
	}
	events := []harness.Event{ev}
	switch m.StopReason {
	case "aborted":
		events = append(events, harness.Event{Kind: harness.EventCancelled, StopReason: m.StopReason})
	case "error":
		events = append(events, harness.Event{Kind: harness.EventError, Err: errors.New(m.ErrorMessage)})
	}
	return events
}

func structured(result json.RawMessage, failed bool, raw []byte) harness.Event {
	var r toolResult
	err := json.Unmarshal(result, &r)
	if failed {
		reason := messageText(r.Content)
		if reason == "" {
			reason = string(result)
		}
		return harness.Event{Kind: harness.EventStructuredRejected, Err: errors.New("pi: respond: " + reason), Raw: raw}
	}
	if err != nil || len(r.Details) == 0 {
		return harness.Event{Kind: harness.EventError, Err: fmt.Errorf("%w: pi: respond returned no details in %s", harness.ErrNoStructuredResponse, result), Raw: raw}
	}
	return harness.Event{Kind: harness.EventStructured, Structured: r.Details, Raw: raw}
}

func extensionError(raw []byte) error {
	var e struct {
		ExtensionPath string `json:"extensionPath"`
		Event         string `json:"event"`
		Error         string `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Error == "" {
		return fmt.Errorf("pi: extension error: %s", raw)
	}
	return fmt.Errorf("pi: extension %s: %s: %s", e.ExtensionPath, e.Event, e.Error)
}

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
