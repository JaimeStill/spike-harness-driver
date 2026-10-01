package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// connection is Claude Code's harness.Connection over a stdio.Client. It answers Claude Code's
// control requests: the MCP messages of the driver's MCP server, which it relays through an
// mcpbridge tunnel, and permission checks.
type connection struct {
	client  *stdio.Client
	tools   *mcpbridge.Server
	tunnel  *mcpbridge.Tunnel
	allowed map[string]bool // the harness tools the session allows
	remove  func() error
	// listWait bounds how long Prompt waits for Claude Code to list the tools again after
	// the respond tool changed.
	listWait time.Duration

	mu sync.Mutex
	// run ends the tool calls of the exchange Cancel cancels.
	run    context.Context
	endRun context.CancelFunc
}

var _ harness.Connection = (*connection)(nil)

func newConnection(tools *mcpbridge.Server, allowed []string, listWait time.Duration) *connection {
	c := &connection{tools: tools, allowed: map[string]bool{}, listWait: listWait}
	for _, t := range allowed {
		c.allowed[t] = true
	}
	c.run, c.endRun = context.WithCancel(context.Background())
	return c
}

// Prompt sends a user message, which Claude Code doesn't answer: the turn's outcome arrives as
// events. A request with a schema first offers the model a respond tool for it, and asks for a
// call in the message.
//
// Claude Code learns of a changed tool list by notification and lists the tools again in its
// own time, so when the respond tool changes, Prompt waits for that listing before it sends the
// message, so the model sees the new tool.
func (c *connection) Prompt(ctx context.Context, req harness.Request) error {
	c.mu.Lock()
	c.endRun()
	c.run, c.endRun = context.WithCancel(context.Background())
	c.mu.Unlock()

	before := c.tools.RespondName()
	if err := c.tools.SetSchema(req.Schema); err != nil {
		return err
	}
	respond := c.tools.RespondName()
	if respond != before {
		if err := c.tools.WaitListed(ctx, c.listWait); err != nil {
			return err
		}
	}

	text := req.Text
	if respond != "" {
		text += "\n\nGive your final answer by calling the " + toolPrefix + respond +
			" tool exactly once, with the answer as its arguments."
	}
	var content []contentBlock
	for _, img := range req.Images {
		content = append(content, contentBlock{Type: "image", Source: &imageSource{
			Type: "base64", MediaType: img.MediaType, Data: base64.StdEncoding.EncodeToString(img.Data),
		}})
	}
	content = append(content, contentBlock{Type: "text", Text: text})
	return c.client.Notify(userMessage{
		Type:    "user",
		Message: userContent{Role: "user", Content: content},
	})
}

// Cancel ends the exchange's tool calls in progress and interrupts the turn. Claude Code
// answers at once, and ends the turn with a result whose terminal reason is "aborted_*".
func (c *connection) Cancel(ctx context.Context) error {
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()
	_, err := c.client.Call(ctx, controlRequest{Subtype: "interrupt"})
	return err
}

func (c *connection) Events() <-chan harness.Event { return c.client.Events() }
func (c *connection) Err() error                   { return c.client.Err() }

// Close ends Claude Code, the MCP server's tunnel, and the session's files.
func (c *connection) Close() error {
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()
	return errors.Join(closeErr(c.client.Close()), c.tunnel.Close(), c.remove())
}

// closeErr is the error of Claude Code's exit once Close asked for it. When the last turn failed,
// an interrupted one included, Claude Code exits with status 1 at the end of its input. The
// exchange of that turn has already reported the failure, so closeErr ignores status 1. A harness
// killed after WaitDelay, or any other status, is still an error.
func closeErr(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil
	}
	return err
}

// outbound relays a message the MCP server sends unasked, such as notifications/tools/list_changed,
// to Claude Code as an mcp_message control request. It runs on the server's goroutine, so it hands
// the message on and returns.
func (c *connection) outbound(msg json.RawMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = c.client.Call(ctx, controlRequest{Subtype: "mcp_message", ServerName: serverName, Message: msg})
	}()
}

// answer answers one of Claude Code's control requests. An error answer is an error reply.
func (c *connection) answer(ctx context.Context, req stdio.Request) any {
	r, ok := req.Body.(controlRequest)
	if !ok {
		return fmt.Errorf("claude: unreadable request")
	}
	switch r.Subtype {
	case "mcp_message":
		return c.relay(ctx, r)
	case "can_use_tool":
		return c.permit(r)
	default:
		return fmt.Errorf("claude: the driver doesn't answer %s", r.Subtype)
	}
}

// notificationAck is the reply to an MCP notification, which has no response of its own; the
// Agent SDKs send the same.
var notificationAck = json.RawMessage(`{"jsonrpc":"2.0","result":{}}`)

// relay passes one MCP message to the MCP server and returns its response. A tool call runs
// until Cancel ends the exchange's run, or until Claude Code exits.
func (c *connection) relay(ctx context.Context, r controlRequest) any {
	if r.ServerName != serverName {
		return fmt.Errorf("claude: no MCP server %q", r.ServerName)
	}
	c.mu.Lock()
	run := c.run
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(run, cancel)()

	resp, err := c.tunnel.Deliver(ctx, r.Message)
	if err != nil {
		return err
	}
	if resp == nil {
		resp = notificationAck
	}
	return map[string]json.RawMessage{"mcp_response": resp}
}

// permit answers a permission check. The driver's own tools, and the harness tools the session
// allows, run; anything else is denied, since no one is there to ask.
func (c *connection) permit(r controlRequest) any {
	if strings.HasPrefix(r.ToolName, toolPrefix) || c.allowed[r.ToolName] {
		input := r.Input
		if len(bytes.TrimSpace(input)) == 0 {
			input = json.RawMessage(`{}`)
		}
		return map[string]any{"behavior": "allow", "updatedInput": input}
	}
	return map[string]any{"behavior": "deny", "message": "the driver doesn't allow " + r.ToolName + " in this session"}
}
