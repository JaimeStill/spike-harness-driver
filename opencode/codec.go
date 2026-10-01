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

// serverName is the name of the driver's MCP server. OpenCode names the server's tools
// <server>_<tool>.
const serverName = "driver"

// toolPrefix starts the name OpenCode gives each of the driver's tools.
const toolPrefix = serverName + "_"

// codec translates ACP as OpenCode speaks it. ACP's updates carry no request ID, but OpenCode
// sends every update of a turn before it answers the turn's session/prompt, so the codec ends
// the turn when it decodes that answer, in stream order. It keeps state across lines: the ids
// of the prompt and load requests in flight, the turn's text, and the tool calls under way. Encode
// runs on callers' goroutines and Decode on the reader's, so the state is locked.
type codec struct {
	mu sync.Mutex
	// prompts and loads hold the ids of the session/prompt and session/load calls in flight.
	prompts map[string]bool
	loads   map[string]bool
	// started is whether the turn has reported EventStarted.
	started bool
	// message and text are the turn's last agent message: its ID, and its text so far.
	message, text string
	// calls maps each tool call's ID to its tool's name, and args to its arguments; called
	// holds the calls already reported.
	calls  map[string]string
	args   map[string]json.RawMessage
	called map[string]bool
}

func newCodec() *codec {
	return &codec{prompts: map[string]bool{}, loads: map[string]bool{},
		calls: map[string]string{}, args: map[string]json.RawMessage{}, called: map[string]bool{}}
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
			c.started, c.message, c.text = false, "", ""
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

// errorData renders a JSON-RPC error's data, which OpenCode uses to name the failing service.
func errorData(data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	return ": " + string(data)
}

// notification maps a notification to events: session/update's, or EventHarness. Updates that
// arrive while a session/load is in flight replay the session's history, and belong to no
// exchange, so they map to EventHarness too.
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

// update maps one session update of a turn. The caller holds c.mu.
func (c *codec) update(u sessionUpdate, line []byte) []harness.Event {
	ev := harness.Event{Kind: harness.EventHarness, Raw: line}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		text, ok := chunkText(u.Content)
		if !ok {
			break
		}
		if u.MessageID != c.message {
			c.message, c.text = u.MessageID, ""
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

// tool maps a tool call's updates. The call surfaces as EventToolCall once it runs, with its
// arguments, and as EventToolResult once it completes or fails. A respond call surfaces only as
// the structured response, or its rejection. The caller holds c.mu.
func (c *codec) tool(u sessionUpdate, line []byte) []harness.Event {
	name, known := c.calls[u.ToolCallID]
	if !known {
		name = toolName(u.Title)
		c.calls[u.ToolCallID] = name
	}
	respond := mcpbridge.IsRespond(name)
	var events []harness.Event
	if len(u.RawInput) > 0 && (string(u.RawInput) != "{}" || u.Status != "pending") {
		c.args[u.ToolCallID] = u.RawInput
	}
	// The call surfaces once OpenCode runs it, or once it ends, whichever is first: a call
	// is pending with no arguments before it runs, and a tool that takes none never has any.
	if !c.called[u.ToolCallID] && u.Status != "pending" && u.Status != "" {
		c.called[u.ToolCallID] = true
		if !respond {
			events = append(events, harness.Event{Kind: harness.EventToolCall, Raw: line,
				Tool: &harness.ToolEvent{CallID: u.ToolCallID, Name: name, Args: c.args[u.ToolCallID]}})
		}
	}
	if u.Status == "completed" || u.Status == "failed" {
		failed := u.Status == "failed"
		args := c.args[u.ToolCallID]
		delete(c.calls, u.ToolCallID)
		delete(c.args, u.ToolCallID)
		delete(c.called, u.ToolCallID)
		// A respond call is accepted when it completed with the bridge's acceptance as its
		// output. The status alone usually decides: OpenCode 1.18.34
		// (packages/opencode/src/mcp/catalog.ts) throws on an MCP result with isError set, which
		// the bridge's rejections carry, so the call fails. The bridge's verdict is checked as
		// well, so the outcome doesn't rest on how another program maps an MCP result to a
		// status.
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

// end maps a session/prompt's answer to the turn's end: EventMessageEnd with the turn's last
// message and its usage, then EventEnded. OpenCode answers a cancelled turn with stop reason
// "cancelled", which maps to "aborted" and EventCancelled, and a failed one with a JSON-RPC
// error, which maps to EventError. The caller holds c.mu.
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
	clear(c.calls)
	clear(c.args)
	clear(c.called)
	c.started = false
	return append(events, harness.Event{Kind: harness.EventEnded})
}

// chunkText is a message chunk's text, when its content is a text block.
func chunkText(content json.RawMessage) (string, bool) {
	var b contentBlock
	if json.Unmarshal(content, &b) != nil || b.Type != "text" {
		return "", false
	}
	return b.Text, true
}

// toolName is the name a tool event carries: a driver tool's own name, without the prefix OpenCode
// gives the driver's server, and a harness tool's name as it is.
func toolName(title string) string {
	return strings.TrimPrefix(title, toolPrefix)
}

// outputText is a tool's output as text: a string as it is, or the JSON.
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
