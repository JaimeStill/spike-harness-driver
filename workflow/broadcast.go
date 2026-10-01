package workflow

import (
	"context"
	"sync"
)

// queue delivers a subscriber's events in order through a channel. It holds any number of
// them, so a run never waits on a slow subscriber and never drops an event for one.
type queue struct {
	out chan Event

	mu     sync.Mutex
	items  []Event
	closed bool
	signal chan struct{}
}

// newQueue starts a queue that delivers until ctx is done, or until it is closed and drained.
func newQueue(ctx context.Context) *queue {
	q := &queue{out: make(chan Event), signal: make(chan struct{}, 1)}
	go q.deliver(ctx)
	return q
}

// push queues events. Events pushed after close are dropped.
func (q *queue) push(events ...Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, events...)
	q.wake()
}

// gone reports whether the queue has stopped delivering, because its subscriber's context
// ended or it was closed and drained.
func (q *queue) gone() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed && len(q.items) == 0
}

// close ends the queue once what it holds is delivered.
func (q *queue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.wake()
}

func (q *queue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *queue) deliver(ctx context.Context) {
	defer close(q.out)
	defer func() {
		// Whatever is still queued is discarded, and later pushes are dropped.
		q.mu.Lock()
		q.closed, q.items = true, nil
		q.mu.Unlock()
	}()
	for {
		q.mu.Lock()
		items, closed := q.items, q.closed
		q.items = nil
		q.mu.Unlock()
		for _, e := range items {
			select {
			case q.out <- e:
			case <-ctx.Done():
				return
			}
		}
		if closed && len(items) == 0 {
			return
		}
		if len(items) > 0 {
			continue
		}
		select {
		case <-q.signal:
		case <-ctx.Done():
			return
		}
	}
}
