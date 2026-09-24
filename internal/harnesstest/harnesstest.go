package harnesstest

import (
	"context"
	"strings"
	"sync"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// SessionID is the ID of every session a Driver opens without Options.SessionID.
const SessionID = "stub-session"

// Reply is the text a Connection answers a prompt with.
const Reply = "Go is a language."

// Driver opens sessions over a Connection.
type Driver struct {
	// Stream marks the prompts that stream until cancelled. Empty streams none.
	Stream string
	// Opened, when set, receives the options each session is opened with.
	Opened func(harness.Options)
}

var _ harness.Driver = Driver{}

func (d Driver) Open(ctx context.Context, opts harness.Options) (*harness.Session, error) {
	if d.Opened != nil {
		d.Opened(opts)
	}
	id := opts.SessionID
	if id == "" {
		id = SessionID
	}
	return harness.NewSession(ctx, id, NewConnection(d.Stream), opts.Store)
}

// Connection is a scripted harness.Connection. Close stops a run in progress before it closes
// Events, so a run never sends on a closed channel.
type Connection struct {
	stream    string
	events    chan harness.Event
	cancel    chan struct{}
	done      chan struct{} // closed by Close
	runs      sync.WaitGroup
	closeOnce sync.Once
}

var _ harness.Connection = (*Connection)(nil)

// NewConnection returns a Connection whose prompts containing stream stream until cancelled.
func NewConnection(stream string) *Connection {
	return &Connection{
		stream: stream,
		events: make(chan harness.Event, 64),
		cancel: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func (c *Connection) Prompt(_ context.Context, req harness.Request) error {
	streaming := c.stream != "" && strings.Contains(req.Text, c.stream)
	c.runs.Add(1)
	go func() {
		defer c.runs.Done()
		c.run(streaming)
	}()
	return nil
}

// send queues ev, and reports false once Close has stopped the run.
func (c *Connection) send(ev harness.Event) bool {
	select {
	case c.events <- ev:
		return true
	case <-c.done:
		return false
	}
}

func (c *Connection) run(streaming bool) {
	if !c.send(harness.Event{Kind: harness.EventStarted}) {
		return
	}
	if !streaming {
		for i, word := range strings.Fields(Reply) {
			if i > 0 {
				word = " " + word
			}
			if !c.send(harness.Event{Kind: harness.EventTextDelta, Text: word}) {
				return
			}
		}
		_ = c.send(harness.Event{Kind: harness.EventMessageEnd, Text: Reply, StopReason: "stop"}) &&
			c.send(harness.Event{Kind: harness.EventEnded})
		return
	}
	for {
		select {
		case <-c.cancel:
			_ = c.send(harness.Event{Kind: harness.EventMessageEnd, StopReason: "aborted"}) &&
				c.send(harness.Event{Kind: harness.EventCancelled, StopReason: "aborted"}) &&
				c.send(harness.Event{Kind: harness.EventEnded})
			return
		case <-c.done:
			return
		case c.events <- harness.Event{Kind: harness.EventTextDelta, Text: "."}:
		}
	}
}

// Cancel ends a streaming run. The session calls it at most once per exchange, and the
// scripted harness is used for one cancellation per connection.
func (c *Connection) Cancel(context.Context) error {
	close(c.cancel)
	return nil
}

func (c *Connection) Events() <-chan harness.Event { return c.events }

func (c *Connection) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.runs.Wait()
		close(c.events)
	})
	return nil
}
