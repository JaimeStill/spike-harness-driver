package harness

import "sync"

// EventQueue delivers events in order through a channel. It holds any number of them, so the
// producer never waits on the consumer: a harness's reader keeps reading while an exchange's
// consumer is slow, or while no one consumes at all.
type EventQueue struct {
	mu      sync.Mutex
	pending []Event
	closed  bool

	wake    chan struct{}
	out     chan Event
	release <-chan struct{}
}

// NewEventQueue starts a queue. Closing release stops delivery at once and discards whatever
// is queued; a nil release never does.
func NewEventQueue(release <-chan struct{}) *EventQueue {
	q := &EventQueue{
		wake:    make(chan struct{}, 1),
		out:     make(chan Event),
		release: release,
	}
	go q.pump()
	return q
}

// Events yields the queued events in order. It closes after Close once every event is
// delivered, or when release closes.
func (q *EventQueue) Events() <-chan Event { return q.out }

// Push queues events. Events pushed after Close are dropped.
func (q *EventQueue) Push(events ...Event) {
	q.mu.Lock()
	if !q.closed {
		q.pending = append(q.pending, events...)
	}
	q.mu.Unlock()
	q.signal()
}

// Close ends the queue: the events already queued are still delivered, and then Events closes.
func (q *EventQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *EventQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *EventQueue) pump() {
	defer close(q.out)
	for {
		q.mu.Lock()
		batch, closed := q.pending, q.closed
		q.pending = nil
		q.mu.Unlock()
		for _, ev := range batch {
			select {
			case q.out <- ev:
			case <-q.release:
				return
			}
		}
		if len(batch) > 0 {
			continue
		}
		if closed {
			return
		}
		select {
		case <-q.wake:
		case <-q.release:
			return
		}
	}
}
