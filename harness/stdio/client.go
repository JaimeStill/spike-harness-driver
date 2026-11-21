package stdio

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Client runs a line-protocol harness through a Codec. It correlates each command with its
// response by id, answers the harness's requests through a Handler, and streams everything
// else as normalized events.
type Client struct {
	p         *Process
	codec     Codec
	handle    Handler
	calls     *calls
	answering sync.WaitGroup
	answers   context.Context
	stop      context.CancelFunc
	events    *harness.EventQueue

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
	id, ch, err := c.send(ctx, cmd)
	if err != nil {
		return Response{}, err
	}
	select {
	case r := <-ch:
		return r, r.Err
	case <-ctx.Done():
		c.calls.forget(id)
		return Response{}, ctx.Err()
	}
}

// Send writes cmd and returns without waiting for its response, which the channel receives
// later: the harness's response, with its error in Err when the harness reports failure, or a
// Response carrying the exit error in Err when the harness exits first. The channel receives
// exactly one Response.
//
// Send is for a command whose response may take as long as a run does, such as a prompt that
// the harness answers only as the turn ends. The command is on the harness's input when Send
// returns, so a command written after it, such as a cancellation, reaches the harness after
// it. Send fails without writing when ctx has ended, or when the harness has exited.
func (c *Client) Send(ctx context.Context, cmd any) (<-chan Response, error) {
	_, ch, err := c.send(ctx, cmd)
	return ch, err
}

func (c *Client) send(ctx context.Context, cmd any) (string, <-chan Response, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	line, err := c.codec.Encode(id, cmd)
	if err != nil {
		return "", nil, err
	}
	ch, err := c.calls.register(id)
	if err != nil {
		return "", nil, err
	}
	if err := c.p.WriteLine(line); err != nil {
		c.calls.forget(id)
		return "", nil, err
	}
	return id, ch, nil
}

// Notify sends cmd without waiting for a response, for a message the harness doesn't answer,
// such as a user message on a stream or a JSON-RPC notification. The codec encodes it with an
// empty id.
func (c *Client) Notify(cmd any) error {
	line, err := c.codec.Encode("", cmd)
	if err != nil {
		return err
	}
	return c.p.WriteLine(line)
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

func (c *Client) read() {
	for line := range c.p.Lines() {
		c.dispatch(line)
	}
	c.shutdown()
}

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

func (c *Client) shutdown() {
	err := c.p.Err()
	if err == nil {
		err = fmt.Errorf("%s: exited", c.p.name)
	}
	c.calls.fail(err)
	c.stop()
	answered := make(chan struct{})
	go func() {
		c.answering.Wait()
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(c.p.waitDelay):
	}
	c.events.Close()
}
