package stdio

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Client runs a line-protocol harness through a Codec. It correlates each command with its
// response by id, whatever order responses arrive in, and streams everything else as
// normalized events.
type Client struct {
	p     *Process
	codec Codec

	nextID  atomic.Int64
	closing atomic.Bool

	mu      sync.Mutex
	pending map[string]chan Response
	exited  bool
	exitErr error

	// Events queue without bound, so a harness that emits events while no one is reading
	// them, as during a start-up handshake, can't stall the responses behind them.
	qmu    sync.Mutex
	queue  []harness.Event
	qdone  bool
	wake   chan struct{}
	events chan harness.Event
}

// NewClient starts reading p through codec.
func NewClient(p *Process, codec Codec) *Client {
	c := &Client{
		p:       p,
		codec:   codec,
		pending: map[string]chan Response{},
		wake:    make(chan struct{}, 1),
		events:  make(chan harness.Event),
	}
	go c.read()
	go c.pump()
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
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.exited {
		c.mu.Unlock()
		return Response{}, c.exitErr
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.p.WriteLine(line); err != nil {
		c.forget(id)
		return Response{}, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return Response{}, c.exitErr
		}
		return r, r.Err
	case <-ctx.Done():
		c.forget(id)
		return Response{}, ctx.Err()
	}
}

// Events yields the harness's normalized events. When the harness exits other than through
// Close, a final EventError carries the exit error. The channel then closes.
func (c *Client) Events() <-chan harness.Event { return c.events }

// Close ends the harness.
func (c *Client) Close() error {
	c.closing.Store(true)
	return c.p.Close()
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) read() {
	for line := range c.p.Lines() {
		f, err := c.codec.Decode(line)
		if err != nil {
			c.enqueue(harness.Event{Kind: harness.EventError, Err: err.Error(), Raw: line})
			continue
		}
		if r := f.Response; r != nil {
			c.mu.Lock()
			ch, ok := c.pending[r.ID]
			delete(c.pending, r.ID)
			c.mu.Unlock()
			if ok {
				ch <- *r
			}
		}
		for _, ev := range f.Events {
			c.enqueue(ev)
		}
	}

	exitErr := c.p.Err()
	unexpected := !c.closing.Load() || exitErr != nil
	if exitErr == nil {
		exitErr = fmt.Errorf("%s: exited", c.p.name)
	}
	c.mu.Lock()
	c.exited = true
	c.exitErr = exitErr
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if unexpected {
		c.enqueue(harness.Event{Kind: harness.EventError, Err: exitErr.Error()})
	}
	c.qmu.Lock()
	c.qdone = true
	c.qmu.Unlock()
	c.signal()
}

func (c *Client) enqueue(ev harness.Event) {
	c.qmu.Lock()
	c.queue = append(c.queue, ev)
	c.qmu.Unlock()
	c.signal()
}

func (c *Client) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// pump delivers queued events in order, and closes Events once read has finished and the
// queue is empty.
func (c *Client) pump() {
	defer close(c.events)
	for {
		c.qmu.Lock()
		batch, done := c.queue, c.qdone
		c.queue = nil
		c.qmu.Unlock()
		for _, ev := range batch {
			c.events <- ev
		}
		if len(batch) == 0 {
			if done {
				return
			}
			<-c.wake
		}
	}
}
