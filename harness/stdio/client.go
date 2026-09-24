package stdio

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Client runs a line-protocol harness through a Codec. It correlates each command with its
// response by id, answers the harness's requests through a Handler, and streams everything
// else as normalized events.
type Client struct {
	p      *Process
	codec  Codec
	handle Handler
	calls  *calls
	// answering counts the requests being answered, which shutdown waits for once it has
	// ended their context, answers.
	answering sync.WaitGroup
	answers   context.Context
	stop      context.CancelFunc
	// Events queue without bound, so a harness that emits events while no one is reading
	// them, as during a start-up handshake, can't stall the responses behind them. The queue
	// lives as long as the process: Close discards what no one read.
	events *harness.EventQueue

	nextID atomic.Int64
}

// NewClient starts reading p through codec, answering the harness's requests with handle. A
// harness waits on its requests, so a codec that decodes any needs a handle; with a nil
// handle, a request goes unanswered and surfaces as an EventError.
func NewClient(p *Process, codec Codec, handle Handler) *Client {
	c := &Client{
		p:      p,
		codec:  codec,
		handle: handle,
		calls:  newCalls(),
		events: harness.NewEventQueue(p.ctx),
	}
	c.answers, c.stop = context.WithCancel(p.ctx)
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

// Events yields the harness's normalized events, and closes once the harness has exited.
func (c *Client) Events() <-chan harness.Event { return c.events.Events() }

// Err reports why the harness exited, once Events has closed: nil when Close ended it, and
// otherwise the exit error, which carries the end of the harness's stderr. It waits for the
// harness to exit.
func (c *Client) Err() error {
	err := c.p.Err()
	cause := c.p.Cause()
	switch {
	case errors.Is(cause, harness.ErrClosed):
		return nil
	case err != nil:
		return err
	case cause != nil:
		return cause
	default:
		return fmt.Errorf("%s: exited", c.p.name)
	}
}

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
	if f.Request != nil {
		c.answer(*f.Request)
	}
	c.events.Push(f.Events...)
}

// answer has the handler answer req on a goroutine of its own, and writes the answer back. An
// answer the harness can no longer take, because it has exited, is dropped.
func (c *Client) answer(req Request) {
	if c.handle == nil {
		c.events.Push(harness.Event{
			Kind: harness.EventError,
			Err:  fmt.Errorf("%s: request %s: no handler to answer it", c.p.name, req.ID),
		})
		return
	}
	c.answering.Go(func() {
		line, err := c.codec.Reply(req, c.handle(c.answers, req))
		if err == nil {
			err = c.p.WriteLine(line)
		}
		if err != nil && c.answers.Err() == nil {
			c.events.Push(harness.Event{
				Kind: harness.EventError, Err: fmt.Errorf("%s: answer request %s: %w", c.p.name, req.ID, err),
			})
		}
	})
}

// shutdown fails the open calls with the exit error, waits for the answers in progress, and
// closes Events. Err reports the exit to whoever reads Events.
func (c *Client) shutdown() {
	err := c.p.Err()
	if err == nil {
		err = fmt.Errorf("%s: exited", c.p.name)
	}
	c.calls.fail(err)
	c.stop()
	c.answering.Wait()
	c.events.Close()
}
