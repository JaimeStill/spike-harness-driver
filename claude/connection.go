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

type connection struct {
	client   *stdio.Client
	tools    *mcpbridge.Server
	tunnel   *mcpbridge.Tunnel
	allowed  map[string]bool
	remove   func() error
	listWait time.Duration

	mu     sync.Mutex
	run    context.Context
	endRun context.CancelFunc
}

var _ harness.Connection = (*connection)(nil)

func newConnection(tools *mcpbridge.Server, allowed []string, listWait time.Duration) *connection {
	c := &connection{tools: tools, listWait: listWait}
	if allowed != nil {
		c.allowed = map[string]bool{}
		for _, t := range allowed {
			c.allowed[t] = true
		}
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

func closeErr(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil
	}
	return err
}

func (c *connection) outbound(msg json.RawMessage) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = c.client.Call(ctx, controlRequest{Subtype: "mcp_message", ServerName: serverName, Message: msg})
	}()
}

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

var notificationAck = json.RawMessage(`{"jsonrpc":"2.0","result":{}}`)

func (c *connection) relay(ctx context.Context, r controlRequest) any {
	if r.ServerName != serverName {
		return fmt.Errorf("claude: no MCP server %q", r.ServerName)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if toolCall(r.Message) {
		c.mu.Lock()
		run := c.run
		c.mu.Unlock()
		defer context.AfterFunc(run, cancel)()
	}

	resp, err := c.tunnel.Deliver(ctx, r.Message)
	if err != nil {
		return err
	}
	if resp == nil {
		resp = notificationAck
	}
	return map[string]json.RawMessage{"mcp_response": resp}
}

func toolCall(msg json.RawMessage) bool {
	var m struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(msg, &m) == nil && m.Method == "tools/call"
}

func (c *connection) permit(r controlRequest) any {
	if strings.HasPrefix(r.ToolName, toolPrefix) || c.allowed == nil || c.allowed[r.ToolName] {
		input := r.Input
		if len(bytes.TrimSpace(input)) == 0 {
			input = json.RawMessage(`{}`)
		}
		return map[string]any{"behavior": "allow", "updatedInput": input}
	}
	return map[string]any{"behavior": "deny", "message": "the driver doesn't allow " + r.ToolName + " in this session"}
}
