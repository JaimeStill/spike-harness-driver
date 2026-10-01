package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// connection is Claude Code's harness.Connection over a stdio.Client. It answers Claude Code's
// control requests: the MCP messages of the driver's tool server, which it relays through an
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
	// gen counts the changes to the respond tool. listedGen is the gen of the last tools/list
	// the tunnel answered, as it stood when the request arrived, so a listing already under way
	// when the tool changed doesn't count as the new one. listed is closed and replaced at each
	// answered listing, so Prompt can wait for the next.
	gen       int
	listedGen int
	listings  int
	listed    chan struct{}
	// run ends the tool calls of the exchange Cancel cancels.
	run    context.Context
	endRun context.CancelFunc
}

var _ harness.Connection = (*connection)(nil)

func newConnection(tools *mcpbridge.Server, allowed []string, listWait time.Duration) *connection {
	c := &connection{tools: tools, allowed: map[string]bool{}, listWait: listWait, listed: make(chan struct{})}
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
// Claude Code learns of a changed tool list by notification and lists the tools again, but it
// connects to the driver's tool server only once the first message arrives. So when the
// respond tool changes on a connected server, Prompt waits for that listing before it sends
// the message, so the model sees the new tool.
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
		c.mu.Lock()
		c.gen++
		gen, connected := c.gen, c.listings > 0
		c.mu.Unlock()
		if connected {
			if err := c.awaitListing(ctx, gen); err != nil {
				return err
			}
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

// awaitListing waits for a tools/list that arrived after the respond tool's gen-th change, for
// up to the connection's listWait. A listing that never comes isn't an error: the model then
// sees the tools Claude Code listed last, and a call to the old respond tool tells it the new
// name.
func (c *connection) awaitListing(ctx context.Context, gen int) error {
	deadline := time.After(c.listWait)
	for {
		c.mu.Lock()
		done, listed := c.listedGen >= gen, c.listed
		c.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-listed:
		case <-deadline:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

// Close ends Claude Code, the tool server's tunnel, and the session's files.
func (c *connection) Close() error {
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()
	return errors.Join(c.client.Close(), c.tunnel.Close(), c.remove())
}

// outbound relays a message the tool server sends unasked, such as
// notifications/tools/list_changed, to Claude Code as an mcp_message control request. It runs
// on the server's goroutine, so it hands the message on and returns.
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

// relay passes one MCP message to the tool server and returns its response. A tool call runs
// until Cancel ends the exchange's run, or until Claude Code exits.
func (c *connection) relay(ctx context.Context, r controlRequest) any {
	if r.ServerName != serverName {
		return fmt.Errorf("claude: no MCP server %q", r.ServerName)
	}
	c.mu.Lock()
	run, gen := c.run, c.gen
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(run, cancel)()

	resp, err := c.tunnel.Deliver(ctx, r.Message)
	if err != nil {
		return err
	}
	if isListing(r.Message) {
		c.mu.Lock()
		c.listings++
		c.listedGen = max(c.listedGen, gen)
		close(c.listed)
		c.listed = make(chan struct{})
		c.mu.Unlock()
	}
	if resp == nil {
		resp = notificationAck
	}
	return map[string]json.RawMessage{"mcp_response": resp}
}

// isListing reports whether msg is a tools/list request.
func isListing(msg json.RawMessage) bool {
	var m struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(msg, &m) == nil && m.Method == "tools/list"
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
