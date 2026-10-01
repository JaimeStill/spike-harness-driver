package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// connection is OpenCode's harness.Connection over a stdio.Client, for one ACP session. It
// answers OpenCode's permission requests; the session's tools run in the driver's MCP server,
// which OpenCode reaches over HTTP.
type connection struct {
	client    *stdio.Client
	sessionID string
	tools     *mcpbridge.Server
	endpoint  *mcpbridge.Endpoint
	// allowed holds the harness tools the session allows; nil allows every tool OpenCode asks
	// about, as the harness's defaults would.
	allowed map[string]bool
	remove  func() error
	// listWait bounds how long Prompt waits for OpenCode to list the tools again after the
	// respond tool changed.
	listWait time.Duration
}

var _ harness.Connection = (*connection)(nil)

// Prompt sends session/prompt, whose answer arrives only as the turn ends, so it returns once
// the request is on its way; the codec turns the answer into the end of the exchange. A request
// with a schema first offers the model a respond tool for it, and asks for a call in the text.
func (c *connection) Prompt(ctx context.Context, req harness.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before := c.tools.RespondName()
	if err := c.tools.SetSchema(req.Schema); err != nil {
		return err
	}
	// OpenCode lists the tools again when the server says they changed, in its own time; a
	// prompt that overtakes the listing reaches a model that doesn't see the new respond tool.
	if c.tools.RespondName() != before {
		if err := c.tools.WaitListed(ctx, c.listWait); err != nil {
			return err
		}
	}
	text := req.Text
	if respond := c.tools.RespondName(); respond != "" {
		text += "\n\nGive your final answer by calling the " + toolPrefix + respond +
			" tool exactly once, with the answer as its arguments."
	}
	prompt := []contentBlock{{Type: "text", Text: text}}
	for _, img := range req.Images {
		prompt = append(prompt, contentBlock{Type: "image", MimeType: img.MediaType, Data: base64.StdEncoding.EncodeToString(img.Data)})
	}
	go func() {
		// The answer, or its failure, reaches the exchange through the codec's events, and the
		// harness's exit through the session.
		_, _ = c.client.Call(context.Background(), call{Method: "session/prompt", Params: map[string]any{
			"sessionId": c.sessionID, "prompt": prompt,
		}})
	}()
	return nil
}

// Cancel sends the session/cancel notification. OpenCode aborts the turn and answers its
// session/prompt with stop reason "cancelled".
func (c *connection) Cancel(context.Context) error {
	return c.client.Notify(call{Method: "session/cancel", Params: map[string]string{"sessionId": c.sessionID}})
}

func (c *connection) Events() <-chan harness.Event { return c.client.Events() }
func (c *connection) Err() error                   { return c.client.Err() }

// Close ends OpenCode, the MCP server's endpoint, and the session's temporary files.
func (c *connection) Close() error {
	return errors.Join(c.client.Close(), c.endpoint.Close(), c.remove())
}

// answer answers one of OpenCode's requests: permission requests by the session's allowlist,
// and anything else, such as fs/write_text_file after an edit, with an error, since the driver
// offers no client capabilities.
func (c *connection) answer(_ context.Context, req stdio.Request) any {
	r, ok := req.Body.(request)
	if !ok {
		return fmt.Errorf("opencode: unreadable request")
	}
	if r.Method != "session/request_permission" {
		return fmt.Errorf("opencode: the driver doesn't answer %s", r.Method)
	}
	var p struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
	}
	_ = json.Unmarshal(r.Params, &p)
	option := "reject"
	if c.permits(p.ToolCall.Title) {
		option = "once"
	}
	return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option}}
}

// permits reports whether the session lets tool run: the driver's own tools always, and a
// harness tool when the allowlist names it, or when there is none.
func (c *connection) permits(tool string) bool {
	return strings.HasPrefix(tool, toolPrefix) || c.allowed == nil || c.allowed[tool]
}
