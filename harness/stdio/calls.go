package stdio

import "sync"

type calls struct {
	mu      sync.Mutex
	pending map[string]chan Response
	failure error
}

func newCalls() *calls {
	return &calls{pending: map[string]chan Response{}}
}

func (c *calls) register(id string) (<-chan Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return nil, c.failure
	}
	ch := make(chan Response, 1)
	c.pending[id] = ch
	return ch, nil
}

func (c *calls) resolve(r Response) {
	c.mu.Lock()
	ch, ok := c.pending[r.ID]
	delete(c.pending, r.ID)
	c.mu.Unlock()
	if ok {
		ch <- r
	}
}

func (c *calls) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *calls) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failure = err
	for id, ch := range c.pending {
		ch <- Response{ID: id, Err: err}
		delete(c.pending, id)
	}
}
