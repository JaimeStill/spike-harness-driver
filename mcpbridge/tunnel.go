package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrTunnelClosed is returned by Deliver once the tunnel is closed.
var ErrTunnelClosed = errors.New("mcpbridge: tunnel is closed")

// Tunnel carries JSON-RPC messages between a harness and the server message by message, for a
// harness that relays them over a channel of its own, as Claude Code relays an sdk MCP
// server's messages over its control protocol.
type Tunnel struct {
	outbound func(json.RawMessage)
	conn     *tunnelConn
	session  *mcp.ServerSession
	remove   func()
	stop     func() bool
	once     sync.Once
}

// TunnelOption configures a Tunnel.
type TunnelOption func(*Tunnel)

// WithOutbound sends the server's own messages, those that answer nothing the harness sent,
// to outbound: notifications such as notifications/tools/list_changed, and requests such as
// ping, whose responses the harness returns through Deliver. Without it they are dropped.
//
// outbound runs on the goroutine sending the message, which waits for it, so it should hand
// the message on rather than wait for the harness. It may be called concurrently.
func WithOutbound(outbound func(msg json.RawMessage)) TunnelOption {
	return func(t *Tunnel) { t.outbound = outbound }
}

// Tunnel connects a new session of the server over a tunnel. The tunnel lasts until Close or
// until ctx ends, and ctx's values reach the session's tool handlers.
func (s *Server) Tunnel(ctx context.Context, opts ...TunnelOption) (*Tunnel, error) {
	t := &Tunnel{}
	for _, o := range opts {
		o(t)
	}
	t.conn = &tunnelConn{
		in:       make(chan jsonrpc.Message),
		outbound: t.outbound,
		closed:   make(chan struct{}),
		waiting:  map[jsonrpc.ID]chan *jsonrpc.Response{},
	}
	srv, remove := s.newServer()
	session, err := srv.Connect(ctx, tunnelTransport{t.conn}, nil)
	if err != nil {
		remove()
		return nil, fmt.Errorf("mcpbridge: connect the tunnel: %w", err)
	}
	t.session, t.remove = session, remove
	t.stop = context.AfterFunc(ctx, func() { _ = t.Close() })
	return t, nil
}

// Deliver gives the server one JSON-RPC message from the harness. For a request it waits for
// the server's response and returns it; when ctx ends first, it tells the server the request
// is cancelled and returns ctx's error. For a notification, or a response to the server's own
// request, it returns nil once the server has taken the message.
func (t *Tunnel) Deliver(ctx context.Context, msg json.RawMessage) (json.RawMessage, error) {
	m, err := jsonrpc.DecodeMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("mcpbridge: decode the message: %w", err)
	}
	req, ok := m.(*jsonrpc.Request)
	if !ok || !req.IsCall() {
		return nil, t.conn.push(ctx, m)
	}

	reply, err := t.conn.await(req.ID)
	if err != nil {
		return nil, err
	}
	if err := t.conn.push(ctx, req); err != nil {
		t.conn.forget(req.ID)
		return nil, err
	}
	select {
	case resp := <-reply:
		out, err := jsonrpc.EncodeMessage(resp)
		if err != nil {
			return nil, fmt.Errorf("mcpbridge: encode the response: %w", err)
		}
		return out, nil
	case <-ctx.Done():
		t.conn.forget(req.ID)
		t.conn.cancel(req.ID, context.Cause(ctx))
		return nil, ctx.Err()
	case <-t.conn.closed:
		return nil, ErrTunnelClosed
	}
}

// Close ends the tunnel's session. A Deliver still waiting returns ErrTunnelClosed.
func (t *Tunnel) Close() error {
	var err error
	t.once.Do(func() {
		t.stop()
		err = t.session.Close()
		t.conn.close()
		t.remove()
	})
	return err
}

// tunnelTransport is the go-sdk Transport of a tunnel, which hands the server conn.
type tunnelTransport struct{ conn *tunnelConn }

func (t tunnelTransport) Connect(context.Context) (mcp.Connection, error) { return t.conn, nil }

// tunnelConn is the server's end of a tunnel. The server reads the messages Deliver pushes;
// what it writes goes to the Deliver waiting on its id when it is a response, and to outbound
// otherwise.
type tunnelConn struct {
	in       chan jsonrpc.Message
	outbound func(json.RawMessage)

	closed    chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	waiting map[jsonrpc.ID]chan *jsonrpc.Response
}

// await registers a wait for the response to the request id. A request id the harness reuses
// while its first request is still open is refused, since the response couldn't be told apart.
func (c *tunnelConn) await(id jsonrpc.ID) (<-chan *jsonrpc.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.waiting[id]; dup {
		return nil, fmt.Errorf("mcpbridge: request %v is already open", id.Raw())
	}
	ch := make(chan *jsonrpc.Response, 1)
	c.waiting[id] = ch
	return ch, nil
}

func (c *tunnelConn) forget(id jsonrpc.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.waiting, id)
}

// push hands m to the server's reader.
func (c *tunnelConn) push(ctx context.Context, m jsonrpc.Message) error {
	select {
	case c.in <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return ErrTunnelClosed
	}
}

// cancel tells the server, as the harness would, that the request id is cancelled, so its
// handler's context ends. It doesn't wait for the server to take the notification.
func (c *tunnelConn) cancel(id jsonrpc.ID, cause error) {
	params := map[string]any{"requestId": id.Raw()}
	if cause != nil {
		params["reason"] = cause.Error()
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	go func() {
		_ = c.push(context.Background(), &jsonrpc.Request{Method: "notifications/cancelled", Params: raw})
	}()
}

func (c *tunnelConn) close() { c.closeOnce.Do(func() { close(c.closed) }) }

// Read implements mcp.Connection.
func (c *tunnelConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case m := <-c.in:
		return m, nil
	case <-c.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Write implements mcp.Connection.
func (c *tunnelConn) Write(_ context.Context, m jsonrpc.Message) error {
	select {
	case <-c.closed:
		return mcp.ErrConnectionClosed
	default:
	}
	if resp, ok := m.(*jsonrpc.Response); ok {
		c.mu.Lock()
		ch, found := c.waiting[resp.ID]
		delete(c.waiting, resp.ID)
		c.mu.Unlock()
		if found {
			ch <- resp
		}
		return nil
	}
	if c.outbound == nil {
		return nil
	}
	raw, err := jsonrpc.EncodeMessage(m)
	if err != nil {
		return err
	}
	c.outbound(raw)
	return nil
}

// Close implements mcp.Connection.
func (c *tunnelConn) Close() error {
	c.close()
	return nil
}

// SessionID implements mcp.Connection. A tunnel carries one session and needs no ID.
func (c *tunnelConn) SessionID() string { return "" }
