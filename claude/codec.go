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

const serverName = "driver"

const toolPrefix = "mcp__" + serverName + "__"

type codec struct {
	respondArgs map[string]json.RawMessage
	toolNames   map[string]string
	limit       *harness.LimitStatus
}

func newCodec() *codec {
	return &codec{respondArgs: map[string]json.RawMessage{}, toolNames: map[string]string{}}
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

func (c *codec) normalize(l line, raw []byte) ([]harness.Event, error) {
	ev := harness.Event{Kind: harness.EventHarness, Raw: raw}
	switch {
	case l.Type == "system" && l.Subtype == "init":
		ev.Kind = harness.EventStarted
		c.limit = nil
	case l.Type == "stream_event":
		var s streamEvent
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("claude: decode %q: %w", raw, err)
		}
		if s.Event.Type != "content_block_delta" {
			break
		}
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
			c.respondArgs[b.ID] = b.Input
			continue
		}
		c.toolNames[b.ID] = name
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
		if args, ok := c.respondArgs[b.ToolUseID]; ok {
			delete(c.respondArgs, b.ToolUseID)
			if text := resultText(b.Content); b.IsError || text != mcpbridge.Accepted {
				events = append(events, harness.Event{Kind: harness.EventStructuredRejected, Raw: raw,
					Err: errors.New("claude: respond: " + text)})
			} else {
				events = append(events, harness.Event{Kind: harness.EventStructured, Structured: args, Raw: raw})
			}
			continue
		}
		name := c.toolNames[b.ToolUseID]
		delete(c.toolNames, b.ToolUseID)
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
	clear(c.respondArgs)
	clear(c.toolNames)
	return append(events, harness.Event{Kind: harness.EventEnded})
}

var limitReset = regexp.MustCompile(`(?i)limit.*\|(\d{9,})`)

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

func toolName(name string) string {
	return strings.TrimPrefix(name, toolPrefix)
}

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
