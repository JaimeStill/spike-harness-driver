package pi

import (
	"context"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// conn is Pi's harness.Conn over a stdio.Client.
type conn struct {
	c *stdio.Client
}

var _ harness.Conn = conn{}

// Prompt sends a prompt. Pi's response only means it accepted the prompt; it rejects a prompt
// while a run is active, which harness.Session never sends.
func (c conn) Prompt(ctx context.Context, req harness.Request) error {
	_, err := c.c.Call(ctx, command{Type: "prompt", Message: req.Text})
	return err
}

// Cancel clears Pi's queue, so no queued message starts another run, and then aborts the run.
// Pi ends the assistant message with stopReason "aborted" and settles, and only then answers
// the abort.
func (c conn) Cancel(ctx context.Context) error {
	if _, err := c.c.Call(ctx, command{Type: "clear_queue"}); err != nil {
		return err
	}
	_, err := c.c.Call(ctx, command{Type: "abort"})
	return err
}

func (c conn) Events() <-chan harness.Event { return c.c.Events() }
func (c conn) Close() error                 { return c.c.Close() }
