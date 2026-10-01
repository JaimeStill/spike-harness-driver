package stdio

import "sync"

// calls correlates commands with their responses by id, whatever order the responses arrive
// in. Each call's channel receives exactly one Response: the harness's, or the failure that
// ended the calls.
type calls struct {
	mu      sync.Mutex
	pending map[string]chan Response
	err     error // set by fail; every later call fails with it
}

func newCalls() *calls {
	return &calls{pending: map[string]chan Response{}}
}

// register opens a call and returns the channel its response arrives on. If the calls fail
// first, the channel receives a Response carrying the failure in Err instead.
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

// fail ends every open call and every later one with err. An open call's channel is still
// empty, since resolve removes a call before it delivers, so the send never blocks.
func (c *calls) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	for id, ch := range c.pending {
		ch <- Response{ID: id, Err: err}
		delete(c.pending, id)
	}
}
