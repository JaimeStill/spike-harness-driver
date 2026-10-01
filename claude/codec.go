package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// serverName is the name of the driver's MCP server. Claude Code names the server's tools
// mcp__<server>__<tool>.
const serverName = "driver"

// toolPrefix starts the name Claude Code gives each of the driver's tools.
const toolPrefix = "mcp__" + serverName + "__"

// codec translates Claude Code's stream-json protocol. Unlike Pi's codec, it keeps state across
// lines, which is safe because the Client has one reader goroutine. It keeps the arguments of each
// respond call, which become the structured response once Claude Code reports the call's result,
// and the last rate-limit notice, which explains a turn the limit refused.
type codec struct {
	// responds maps each respond call's tool_use ID to its arguments. calls maps the ID of every
	// other call to its tool's name, which Claude Code's tool results don't carry.
	responds map[string]json.RawMessage
	calls    map[string]string
	limit    *harness.LimitStatus
}

func newCodec() *codec {
	return &codec{responds: map[string]json.RawMessage{}, calls: map[string]string{}}
}

// Encode renders a control request, which carries id as its request_id, or a user message, which
// carries none because Claude Code doesn't answer it.
func (c *codec) Encode(id string, cmd any) ([]byte, error) {
	switch cmd := cmd.(type) {
	case controlRequest:
		req, err := json.Marshal(cmd)
		if err != nil {
			return nil, err
		}
		return json.Marshal(controlEnvelope{Type: "control_request", RequestID: id, Request: req})
	case userMessage:
		return json.Marshal(cmd)
	default:
		return nil, fmt.Errorf("claude: encode %T: not a command", cmd)
	}
}

// Reply answers one of Claude Code's control requests: an error answer is an error reply, and
// anything else is the success reply's response.
func (c *codec) Reply(req stdio.Request, answer any) ([]byte, error) {
	reply := controlReply{Subtype: "success", RequestID: req.ID}
	if err, ok := answer.(error); ok {
		reply.Subtype, reply.Error = "error", err.Error()
	} else {
		data, err := json.Marshal(answer)
		if err != nil {
			return nil, err
		}
		reply.Response = data
	}
	return json.Marshal(controlResponse{Type: "control_response", Response: reply})
}

// Decode turns a control response into a Response, a control request into a Request, and any
// other line into normalized events.
func (c *codec) Decode(raw []byte) (stdio.Frame, error) {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		return stdio.Frame{}, fmt.Errorf("claude: decode %q: %w", raw, err)
	}
	switch l.Type {
	case "control_response":
		var r controlResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			return stdio.Frame{}, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		resp := &stdio.Response{ID: r.Response.RequestID, Data: r.Response.Response}
		if r.Response.Subtype == "error" {
			resp.Err = errors.New("claude: " + r.Response.Error)
		}
		return stdio.Frame{Response: resp}, nil
	case "control_request":
		var env controlEnvelope
		var req controlRequest
		if err := json.Unmarshal(raw, &env); err != nil {
			return stdio.Frame{}, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return stdio.Frame{}, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		return stdio.Frame{Request: &stdio.Request{ID: env.RequestID, Body: req}}, nil
	}
	events, err := c.normalize(l, raw)
	if err != nil {
		return stdio.Frame{}, err
	}
	return stdio.Frame{Events: events}, nil
}

// normalize maps one stream-json line to normalized events. Every line maps to at least one,
// so nothing Claude Code reports is lost: lines with no normalized meaning become EventHarness.
//
// A turn starts at the system init message, which Claude Code sends as each turn begins, and
// ends at its result, which maps to EventMessageEnd and EventEnded. Claude Code accepts a user
// message during a turn and runs it as a later turn, but harness.Session never sends one.
// Text and thinking arrive as stream deltas; the assistant messages that follow repeat them
// whole, so only their tool calls are read.
func (c *codec) normalize(l line, raw []byte) ([]harness.Event, error) {
	ev := harness.Event{Kind: harness.EventHarness, Raw: raw}
	switch {
	case l.Type == "system" && l.Subtype == "init":
		ev.Kind = harness.EventStarted
	case l.Type == "stream_event":
		var s streamEvent
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		if s.Event.Type != "content_block_delta" {
			break
		}
		// A model whose thinking Claude Code doesn't display streams empty thinking deltas.
		switch d := s.Event.Delta; {
		case d.Type == "text_delta" && d.Text != "":
			ev.Kind, ev.Text = harness.EventTextDelta, d.Text
		case d.Type == "thinking_delta" && d.Thinking != "":
			ev.Kind, ev.Text = harness.EventThinkingDelta, d.Thinking
		}
	case l.Type == "assistant":
		return c.toolCalls(raw)
	case l.Type == "user":
		return c.toolResults(raw)
	case l.Type == "rate_limit_event":
		var r rateLimit
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		c.limit = limitStatus(r)
		ev.Kind, ev.Limit = harness.EventLimit, c.limit
	case l.Type == "result":
		var r result
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		return c.end(r, raw), nil
	}
	return []harness.Event{ev}, nil
}

// toolCalls maps an assistant message's tool_use blocks to EventToolCall. A respond call is
// kept for its result instead: it is how the driver gets a structured response, not a tool of
// the session's.
func (c *codec) toolCalls(raw []byte) ([]harness.Event, error) {
	blocks, err := blocksOf(raw)
	if err != nil {
		return nil, err
	}
	var events []harness.Event
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		name := toolName(b.Name)
		if mcpbridge.IsRespond(name) && strings.HasPrefix(b.Name, toolPrefix) {
			c.responds[b.ID] = b.Input
			continue
		}
		c.calls[b.ID] = name
		events = append(events, harness.Event{
			Kind: harness.EventToolCall, Raw: raw,
			Tool: &harness.ToolEvent{CallID: b.ID, Name: name, Args: b.Input},
		})
	}
	if len(events) == 0 {
		events = []harness.Event{{Kind: harness.EventHarness, Raw: raw}}
	}
	return events, nil
}

// toolResults maps a user message's tool_result blocks to EventToolResult. A respond call's result
// becomes the structured response when mcpbridge accepted its arguments, which it validated against
// the exchange's schema, and an EventStructuredRejected with the rejection the model saw otherwise.
func (c *codec) toolResults(raw []byte) ([]harness.Event, error) {
	blocks, err := blocksOf(raw)
	if err != nil {
		return nil, err
	}
	var events []harness.Event
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		if args, ok := c.responds[b.ToolUseID]; ok {
			delete(c.responds, b.ToolUseID)
			if b.IsError {
				events = append(events, harness.Event{Kind: harness.EventStructuredRejected, Raw: raw,
					Err: errors.New("claude: respond: " + resultText(b.Content))})
			} else {
				events = append(events, harness.Event{Kind: harness.EventStructured, Structured: args, Raw: raw})
			}
			continue
		}
		name := c.calls[b.ToolUseID]
		delete(c.calls, b.ToolUseID)
		events = append(events, harness.Event{
			Kind: harness.EventToolResult, Raw: raw,
			Tool: &harness.ToolEvent{CallID: b.ToolUseID, Name: name, Result: b.Content, IsError: b.IsError},
		})
	}
	if len(events) == 0 {
		events = []harness.Event{{Kind: harness.EventHarness, Raw: raw}}
	}
	return events, nil
}

// end maps a turn's result to EventMessageEnd, with the turn's text and its usage summed over
// its model requests, and EventEnded. Claude Code reports an interrupted turn as an error
// whose terminal reason starts with "aborted", which maps to the stop reason "aborted" and
// EventCancelled; any other error is EventError, and a *harness.LimitError when the
// subscription's usage limit refused the turn.
func (c *codec) end(r result, raw []byte) []harness.Event {
	stop := ""
	if r.StopReason != nil {
		stop = *r.StopReason
	}
	aborted := strings.HasPrefix(r.TerminalReason, "aborted")
	switch {
	case aborted:
		stop = "aborted"
	case r.IsError:
		stop = "error"
	}
	end := harness.Event{Kind: harness.EventMessageEnd, Text: r.Result, StopReason: stop, Raw: raw}
	if u := r.Usage; u != nil {
		end.Usage = &harness.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
	}
	events := []harness.Event{end}
	switch {
	case aborted:
		events = append(events, harness.Event{Kind: harness.EventCancelled, StopReason: stop})
	case r.IsError:
		events = append(events, harness.Event{Kind: harness.EventError, Err: c.turnError(r)})
	}
	clear(c.responds)
	clear(c.calls)
	return append(events, harness.Event{Kind: harness.EventEnded})
}

// limitReset finds the reset time in Claude Code's usage-limit message, which ends with
// "|<unix seconds>".
var limitReset = regexp.MustCompile(`(?i)limit.*\|(\d{9,})`)

// turnError is a failed turn's error. A turn the subscription's limit refused, by a rejected
// rate-limit notice, an HTTP 429, or a usage-limit message, is a *harness.LimitError, reset
// when the notice or the message says.
func (c *codec) turnError(r result) error {
	msg := strings.Join(r.Errors, "; ")
	if msg == "" {
		msg = r.Result
	}
	if msg == "" {
		msg = r.Subtype
	}
	limited := c.limit != nil && c.limit.Status == "rejected"
	limited = limited || (r.APIErrorStatus != nil && *r.APIErrorStatus == 429)
	var resets time.Time
	if m := limitReset.FindStringSubmatch(msg); m != nil {
		limited = true
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			resets = time.Unix(sec, 0)
		}
	}
	if !limited {
		return errors.New("claude: " + msg)
	}
	if resets.IsZero() && c.limit != nil {
		resets = c.limit.ResetsAt
	}
	return &harness.LimitError{ResetsAt: resets, Message: msg}
}

// limitStatus is a rate_limit_event's notice. The utilization is the limiting window's.
func limitStatus(r rateLimit) *harness.LimitStatus {
	s := &harness.LimitStatus{Status: r.Info.Status, Kind: r.Info.RateLimitType}
	if r.Info.ResetsAt > 0 {
		s.ResetsAt = time.Unix(r.Info.ResetsAt, 0)
	}
	if w, ok := r.Info.UnifiedWindows[r.Info.RateLimitType]; ok {
		s.Utilization = w.Utilization
	}
	return s
}

// blocksOf decodes a message's content blocks. A user message's content may be a plain string,
// which holds no blocks.
func blocksOf(raw []byte) ([]contentBlock, error) {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("claude: decode %q: %w", raw, err)
	}
	var blocks []contentBlock
	if json.Unmarshal(m.Message.Content, &blocks) != nil {
		return nil, nil
	}
	return blocks, nil
}

// toolName is the name a tool event carries: a driver tool's own name, without the prefix
// Claude Code gives the driver's server, and a harness tool's name as it is.
func toolName(name string) string {
	return strings.TrimPrefix(name, toolPrefix)
}

// resultText is a tool result's text, from a string or the text blocks of a list.
func resultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []contentBlock
	_ = json.Unmarshal(content, &blocks)
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
