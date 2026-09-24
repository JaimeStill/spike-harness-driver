package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// connection is Pi's harness.Connection over a stdio.Client.
type connection struct {
	client *stdio.Client
}

var (
	_ harness.Connection = connection{}
	_ harness.Journal    = connection{}
)

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

// Head returns the ID of the last entry in Pi's session, in append order.
func (c connection) Head(ctx context.Context) (string, error) {
	ids, err := c.Since(ctx, "")
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[len(ids)-1], nil
}

// Since returns the IDs of the entries Pi appended after id. Pi's entry IDs are stable across
// processes, and Pi fails get_entries when since names no entry it holds, which Since
// reports as harness.ErrUnknownEntry.
func (c connection) Since(ctx context.Context, id string) ([]string, error) {
	r, err := c.client.Call(ctx, command{Type: "get_entries", Since: id})
	var failed *commandError
	if id != "" && errors.As(err, &failed) {
		return nil, fmt.Errorf("%w: %w", harness.ErrUnknownEntry, err)
	}
	if err != nil {
		return nil, err
	}
	return entryIDs(r.Data)
}

// entryIDs reads the entry IDs from a get_entries response's data.
func entryIDs(data json.RawMessage) ([]string, error) {
	var e entries
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("pi: get_entries: %w", err)
	}
	ids := make([]string, len(e.Entries))
	for i, entry := range e.Entries {
		ids[i] = entry.ID
	}
	return ids, nil
}
