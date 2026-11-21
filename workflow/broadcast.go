package workflow

import (
	"context"
	"sync"
)

type queue struct {
	out chan Event

	mu     sync.Mutex
	items  []Event
	closed bool
	signal chan struct{}
}

func newQueue(ctx context.Context) *queue {
	q := &queue{out: make(chan Event), signal: make(chan struct{}, 1)}
	go q.deliver(ctx)
	return q
}

func (q *queue) push(events ...Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, events...)
	q.wake()
}

func (q *queue) gone() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed && len(q.items) == 0
}

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
