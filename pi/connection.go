package pi

import (
	"context"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// connection is Pi's harness.Connection over a stdio.Client.
type connection struct {
	client *stdio.Client
}

var _ harness.Connection = connection{}

// Prompt sends a prompt. Pi's response only means it accepted the prompt; it rejects a prompt
// while a run is active, which harness.Session never sends.
func (c connection) Prompt(ctx context.Context, req harness.Request) error {
	_, err := c.client.Call(ctx, command{Type: "prompt", Message: req.Text})
	return err
}

// Cancel clears Pi's queue, so no queued message starts another run, and then aborts the run.
// Pi ends the assistant message with stopReason "aborted" and settles, and only then answers
// the abort.
func (c connection) Cancel(ctx context.Context) error {
	if _, err := c.client.Call(ctx, command{Type: "clear_queue"}); err != nil {
		return err
	}
	_, err := c.client.Call(ctx, command{Type: "abort"})
	return err
}

func (c connection) Events() <-chan harness.Event { return c.client.Events() }
func (c connection) Close() error                 { return c.client.Close() }
