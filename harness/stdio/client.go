package stdio

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Client runs a line-protocol harness through a Codec. It correlates each command with its
// response by id, and streams everything else as normalized events.
type Client struct {
	p     *Process
	codec Codec
	calls *calls
	// Events queue without bound, so a harness that emits events while no one is reading
	// them, as during a start-up handshake, can't stall the responses behind them. The queue
	// lives as long as the process: Close discards what no one read.
	events *harness.EventQueue

	nextID atomic.Int64
}

// NewClient starts reading p through codec.
func NewClient(p *Process, codec Codec) *Client {
	c := &Client{
		p:      p,
		codec:  codec,
		calls:  newCalls(),
		events: harness.NewEventQueue(p.ctx),
	}
	go c.read()
	return c
}

// Call sends cmd and waits for its response. It fails with the response's error when the
// harness reports failure, and with the exit error when the harness exits first.
func (c *Client) Call(ctx context.Context, cmd any) (Response, error) {
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	line, err := c.codec.Encode(id, cmd)
	if err != nil {
		return Response{}, err
	}
	ch, err := c.calls.register(id)
	if err != nil {
		return Response{}, err
	}
	if err := c.p.WriteLine(line); err != nil {
		c.calls.forget(id)
		return Response{}, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return Response{}, c.calls.failure()
		}
		return r, r.Err
	case <-ctx.Done():
		c.calls.forget(id)
		return Response{}, ctx.Err()
	}
}

// Events yields the harness's normalized events. When the harness exits other than through
// Close, a final EventError carries the exit error. The channel then closes.
func (c *Client) Events() <-chan harness.Event { return c.events.Events() }

// Close ends the harness.
func (c *Client) Close() error { return c.p.Close() }

// read dispatches each line the harness writes, then shuts the client down once the harness
// has exited.
func (c *Client) read() {
	for line := range c.p.Lines() {
		c.dispatch(line)
	}
	c.shutdown()
}

// dispatch decodes one line, resolving the call it answers or queueing the events it carries.
func (c *Client) dispatch(line []byte) {
	f, err := c.codec.Decode(line)
	if err != nil {
		c.events.Push(harness.Event{Kind: harness.EventError, Err: err, Raw: line})
		return
	}
	if f.Response != nil {
		c.calls.resolve(*f.Response)
	}
	c.events.Push(f.Events...)
}

// shutdown fails the open calls with the exit error, announces an exit no one asked for, and
// closes Events.
func (c *Client) shutdown() {
	err := c.p.Err()
	unexpected := err != nil || c.p.Cause() == nil
	if err == nil {
		err = fmt.Errorf("%s: exited", c.p.name)
	}
	c.calls.fail(err)
	if unexpected {
		c.events.Push(harness.Event{Kind: harness.EventError, Err: err})
	}
	c.events.Close()
}
