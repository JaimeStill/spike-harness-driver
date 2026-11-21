package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

const serverName = "driver"

const toolPrefix = serverName + "_"

type codec struct {
	mu              sync.Mutex
	prompts         map[string]bool
	loads           map[string]bool
	started         bool
	messageID, text string
	toolNames       map[string]string
	toolArgs        map[string]json.RawMessage
	reported        map[string]bool
}

func newCodec() *codec {
	return &codec{prompts: map[string]bool{}, loads: map[string]bool{},
		toolNames: map[string]string{}, toolArgs: map[string]json.RawMessage{}, reported: map[string]bool{}}
}

// Encode renders a call as a JSON-RPC request with a numeric id, or as a notification for an
// empty id.
func (c *codec) Encode(id string, cmd any) ([]byte, error) {
	cl, ok := cmd.(call)
	if !ok {
		return nil, fmt.Errorf("opencode: encode %T: not a call", cmd)
	}
	msg := map[string]any{"jsonrpc": "2.0", "method": cl.Method, "params": cl.Params}
	if id != "" {
		msg["id"] = json.RawMessage(id)
		c.mu.Lock()
		switch cl.Method {
		case "session/prompt":
			c.prompts[id] = true
			c.started, c.messageID, c.text = false, "", ""
		case "session/load":
			c.loads[id] = true
		}
		c.mu.Unlock()
	}
	return json.Marshal(msg)
}

// Reply answers one of OpenCode's requests: an error answer is a JSON-RPC error, and anything
// else is the result. The request's id is echoed as it arrived.
func (c *codec) Reply(req stdio.Request, answer any) ([]byte, error) {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID)}
	if err, ok := answer.(error); ok {
		msg["error"] = rpcError{Code: -32603, Message: err.Error()}
	} else {
		msg["result"] = answer
	}
	return json.Marshal(msg)
}

// Decode turns a response into a Response, a request into a Request, and a notification into
// normalized events. A session/prompt's response also ends the turn.
func (c *codec) Decode(line []byte) (stdio.Frame, error) {
	var m rpcMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return stdio.Frame{}, fmt.Errorf("opencode: decode %q: %w", line, err)
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		return stdio.Frame{Request: &stdio.Request{ID: string(m.ID), Body: request{Method: m.Method, Params: m.Params}}}, nil
	case m.Method != "":
		return stdio.Frame{Events: c.notification(m, line)}, nil
	}
	id := string(m.ID)
	resp := &stdio.Response{ID: id, Data: m.Result}
	if m.Error != nil {
		resp.Err = fmt.Errorf("opencode: %s (%d)%s", m.Error.Message, m.Error.Code, errorData(m.Error.Data))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.loads, id)
	if !c.prompts[id] {
		return stdio.Frame{Response: resp}, nil
	}
	delete(c.prompts, id)
	return stdio.Frame{Response: resp, Events: c.end(m, line)}, nil
}

func errorData(data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	return ": " + string(data)
}

func (c *codec) notification(m rpcMessage, line []byte) []harness.Event {
	harnessEvent := []harness.Event{{Kind: harness.EventHarness, Raw: line}}
	if m.Method != "session/update" {
		return harnessEvent
	}
	var p struct {
		Update sessionUpdate `json:"update"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return []harness.Event{{Kind: harness.EventError, Err: fmt.Errorf("opencode: decode %q: %w", line, err), Raw: line}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.loads) > 0 || len(c.prompts) == 0 {
		return harnessEvent
	}
	events := c.update(p.Update, line)
	if !c.started {
		c.started = true
		events = append([]harness.Event{{Kind: harness.EventStarted}}, events...)
	}
	return events
}

func (c *codec) update(u sessionUpdate, line []byte) []harness.Event {
	ev := harness.Event{Kind: harness.EventHarness, Raw: line}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		text, ok := chunkText(u.Content)
		if !ok {
			break
		}
		if u.MessageID != c.messageID {
			c.messageID, c.text = u.MessageID, ""
		}
		c.text += text
		ev.Kind, ev.Text = harness.EventTextDelta, text
	case "agent_thought_chunk":
		if text, ok := chunkText(u.Content); ok {
			ev.Kind, ev.Text = harness.EventThinkingDelta, text
		}
	case "tool_call", "tool_call_update":
		return c.tool(u, line)
	}
	return []harness.Event{ev}
}

func (c *codec) tool(u sessionUpdate, line []byte) []harness.Event {
	name, known := c.toolNames[u.ToolCallID]
	if !known {
		name = toolName(u.Title)
		c.toolNames[u.ToolCallID] = name
	}
	respond := mcpbridge.IsRespond(name)
	var events []harness.Event
	if len(u.RawInput) > 0 && (string(u.RawInput) != "{}" || u.Status != "pending") {
		c.toolArgs[u.ToolCallID] = u.RawInput
	}
	if !c.reported[u.ToolCallID] && u.Status != "pending" && u.Status != "" {
		c.reported[u.ToolCallID] = true
		if !respond {
			events = append(events, harness.Event{Kind: harness.EventToolCall, Raw: line,
				Tool: &harness.ToolEvent{CallID: u.ToolCallID, Name: name, Args: c.toolArgs[u.ToolCallID]}})
		}
	}
	if u.Status == "completed" || u.Status == "failed" {
		failed := u.Status == "failed"
		args := c.toolArgs[u.ToolCallID]
		delete(c.toolNames, u.ToolCallID)
		delete(c.toolArgs, u.ToolCallID)
		delete(c.reported, u.ToolCallID)
		switch output := outputText(u.RawOutput); {
		case respond && (failed || output != mcpbridge.Accepted):
			events = append(events, harness.Event{Kind: harness.EventStructuredRejected, Raw: line,
				Err: errors.New("opencode: respond: " + output)})
		case respond:
			events = append(events, harness.Event{Kind: harness.EventStructured, Structured: args, Raw: line})
		default:
			events = append(events, harness.Event{Kind: harness.EventToolResult, Raw: line,
				Tool: &harness.ToolEvent{CallID: u.ToolCallID, Name: name, Result: u.RawOutput, IsError: failed}})
		}
	}
	if len(events) == 0 {
		events = []harness.Event{{Kind: harness.EventHarness, Raw: line}}
	}
	return events
}

func (c *codec) end(m rpcMessage, line []byte) []harness.Event {
	var events []harness.Event
	if !c.started {
		events = append(events, harness.Event{Kind: harness.EventStarted})
	}
	end := harness.Event{Kind: harness.EventMessageEnd, Text: c.text, Raw: line}
	var r promptResult
	switch {
	case m.Error != nil:
		end.StopReason = "error"
	case json.Unmarshal(m.Result, &r) != nil:
		end.StopReason = "error"
	default:
		end.StopReason = r.StopReason
		if r.StopReason == "cancelled" {
			end.StopReason = "aborted"
		}
		if u := r.Usage; u != nil {
			end.Usage = &harness.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
		}
	}
	events = append(events, end)
	switch {
	case end.StopReason == "aborted":
		events = append(events, harness.Event{Kind: harness.EventCancelled, StopReason: end.StopReason})
	case m.Error != nil:
		events = append(events, harness.Event{Kind: harness.EventError,
			Err: fmt.Errorf("opencode: %s (%d)%s", m.Error.Message, m.Error.Code, errorData(m.Error.Data))})
	case end.StopReason == "error":
		events = append(events, harness.Event{Kind: harness.EventError, Err: fmt.Errorf("opencode: unreadable prompt result %s", m.Result)})
	}
	clear(c.toolNames)
	clear(c.toolArgs)
	clear(c.reported)
	c.started = false
	return append(events, harness.Event{Kind: harness.EventEnded})
}

func chunkText(content json.RawMessage) (string, bool) {
	var b contentBlock
	if json.Unmarshal(content, &b) != nil || b.Type != "text" {
		return "", false
	}
	return b.Text, true
}

func toolName(title string) string {
	return strings.TrimPrefix(title, toolPrefix)
}

func outputText(out json.RawMessage) string {
	var s string
	if json.Unmarshal(out, &s) == nil {
		return s
	}
	var o struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(out, &o) == nil && (o.Output != "" || o.Error != "") {
		return o.Error + o.Output
	}
	return string(out)
}
