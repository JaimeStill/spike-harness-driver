package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// connection is Pi's harness.Connection over a stdio.Client. It answers the bridge's dialogs:
// tool calls, and the schema of the exchange whose run is starting.
type connection struct {
	client *stdio.Client
	bridge *bridge

	mu sync.Mutex
	// schema is the open exchange's, which the bridge asks for as each run starts.
	schema json.RawMessage
	// run ends the tool calls of the exchange Cancel cancels.
	run    context.Context
	endRun context.CancelFunc
}

var (
	_ harness.Connection = (*connection)(nil)
	_ harness.Journal    = (*connection)(nil)
)

func newConnection(b *bridge) *connection {
	c := &connection{bridge: b}
	c.run, c.endRun = context.WithCancel(context.Background())
	return c
}

// Prompt sends a prompt. Pi's response only means it accepted the prompt; it rejects a prompt
// while a run is active, which harness.Session never sends. The request's schema is kept for
// the bridge, which asks for it before each run of the exchange.
func (c *connection) Prompt(ctx context.Context, req harness.Request) error {
	c.mu.Lock()
	c.schema = req.Schema
	c.endRun()
	c.run, c.endRun = context.WithCancel(context.Background())
	c.mu.Unlock()
	_, err := c.client.Call(ctx, command{Type: "prompt", Message: req.Text})
	return err
}

// Cancel ends the exchange's tool calls in progress, clears Pi's queue, so no queued message
// starts another run, and then aborts the run. Pi ends the assistant message with stopReason
// "aborted" and settles, and only then answers the abort.
func (c *connection) Cancel(ctx context.Context) error {
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()
	if _, err := c.client.Call(ctx, command{Type: "clear_queue"}); err != nil {
		return err
	}
	_, err := c.client.Call(ctx, command{Type: "abort"})
	return err
}

func (c *connection) Events() <-chan harness.Event { return c.client.Events() }
func (c *connection) Err() error                   { return c.client.Err() }

// Close ends Pi and removes the bridge's files.
func (c *connection) Close() error {
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()
	return errors.Join(c.client.Close(), c.bridge.remove())
}

// answer answers one of Pi's dialogs. The bridge's are input dialogs: a tool call, answered
// once the tool has run, and the exchange's schema. Any other dialog is dismissed, since no
// one is there to answer it, and a dialog left waiting would hold up the run.
func (c *connection) answer(ctx context.Context, req stdio.Request) any {
	d, ok := req.Body.(dialog)
	if !ok || d.Method != "input" {
		return nil
	}
	switch d.Title {
	case callTitle:
		c.mu.Lock()
		run := c.run
		c.mu.Unlock()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(run, cancel)()
		return c.bridge.answerCall(ctx, d.Placeholder)
	case exchangeTitle:
		c.mu.Lock()
		defer c.mu.Unlock()
		return answerExchange(c.schema)
	default:
		return nil
	}
}

// Head returns the ID of the last entry in Pi's session, in append order.
func (c *connection) Head(ctx context.Context) (string, error) {
	ids, err := c.Since(ctx, "")
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[len(ids)-1], nil
}

// Since returns the IDs of the entries Pi appended after id. Pi's entry IDs are stable across
// processes, and Pi fails get_entries when since names no entry it holds, which Since
// reports as harness.ErrUnknownEntry.
//
// Since maps any refusal of a get_entries with since to ErrUnknownEntry, rather than matching
// Pi's message ("Entry not found: <id>"), because unknown entries are the one refusal Pi
// documents for it. A Pi without get_entries refuses it first without since, in Head, when
// the session opens, so it surfaces as that error and never as a lost session.
func (c *connection) Since(ctx context.Context, id string) ([]string, error) {
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
