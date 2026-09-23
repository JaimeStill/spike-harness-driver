package stdio

import "sync"

// calls correlates commands with their responses by id, whatever order the responses arrive
// in.
type calls struct {
	mu      sync.Mutex
	pending map[string]chan Response
	err     error // set by fail; every later call fails with it
}

func newCalls() *calls {
	return &calls{pending: map[string]chan Response{}}
}

// register opens a call and returns the channel its response arrives on. The channel closes
// without a response if the calls fail first.
func (c *calls) register(id string) (<-chan Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	ch := make(chan Response, 1)
	c.pending[id] = ch
	return ch, nil
}

// resolve delivers r to the call with r's id. A response to no open call is dropped.
func (c *calls) resolve(r Response) {
	c.mu.Lock()
	ch, ok := c.pending[r.ID]
	delete(c.pending, r.ID)
	c.mu.Unlock()
	if ok {
		ch <- r
	}
}

// forget drops a call its caller stopped waiting for.
func (c *calls) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// fail ends every open call and every later one with err.
func (c *calls) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// failure returns the error the calls failed with.
func (c *calls) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
