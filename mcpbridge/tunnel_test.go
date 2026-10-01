package mcpbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// wait bounds every wait in these tests.
const wait = 5 * time.Second

// rawClient speaks JSON-RPC over a tunnel as a harness does: a message at a time, with the
// server's own messages arriving on outbound.
type rawClient struct {
	t        *testing.T
	tunnel   *mcpbridge.Tunnel
	outbound chan json.RawMessage
	nextID   atomic.Int64
}

func newRawClient(t *testing.T, s *mcpbridge.Server) *rawClient {
	t.Helper()
	c := &rawClient{t: t, outbound: make(chan json.RawMessage, 64)}
	tunnel, err := s.Tunnel(t.Context(), mcpbridge.WithOutbound(func(m json.RawMessage) {
		select {
		case c.outbound <- m:
		default:
			t.Errorf("outbound full, dropped %s", m)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })
	c.tunnel = tunnel
	return c
}

// call sends a request and returns its result, failing the test on a protocol error.
func (c *rawClient) call(method string, params any) json.RawMessage {
	c.t.Helper()
	resp, err := c.request(method, params)
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	return resp
}

// request sends a request and returns its result or its protocol error.
func (c *rawClient) request(method string, params any) (json.RawMessage, error) {
	c.t.Helper()
	id := c.nextID.Add(1)
	msg, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(c.t.Context(), wait)
	defer cancel()
	raw, err := c.tunnel.Deliver(ctx, msg)
	if err != nil {
		c.t.Fatalf("%s: deliver: %v", method, err)
	}
	var resp struct {
		ID     int64           `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.t.Fatalf("%s: response %s: %v", method, raw, err)
	}
	if resp.ID != id {
		c.t.Fatalf("%s: response id %d, want %d", method, resp.ID, id)
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	return resp.Result, nil
}

// notify sends a notification, which Deliver must answer with nil.
func (c *rawClient) notify(method string) {
	c.t.Helper()
	msg := fmt.Appendf(nil, `{"jsonrpc":"2.0","method":%q}`, method)
	raw, err := c.tunnel.Deliver(c.t.Context(), msg)
	if err != nil || raw != nil {
		c.t.Fatalf("%s: Deliver = %s, %v; want nil, nil", method, raw, err)
	}
}

// initialize runs the handshake at protocol version 2025-11-25, before the subscriptions of
// 2026-07-28, as a harness of that version does.
func (c *rawClient) initialize() {
	c.t.Helper()
	c.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})
	c.notify("notifications/initialized")
}

type listedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (c *rawClient) tools() map[string]listedTool {
	c.t.Helper()
	var res struct{ Tools []listedTool }
	if err := json.Unmarshal(c.call("tools/list", map[string]any{}), &res); err != nil {
		c.t.Fatal(err)
	}
	m := map[string]listedTool{}
	for _, t := range res.Tools {
		m[t.Name] = t
	}
	return m
}

// toolResult is a tools/call result: its text and whether it failed.
type toolResult struct {
	text    string
	isError bool
}

func (c *rawClient) callTool(name string, args any) toolResult {
	c.t.Helper()
	var res struct {
		Content []struct{ Text string }
		IsError bool
	}
	raw := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if err := json.Unmarshal(raw, &res); err != nil {
		c.t.Fatal(err)
	}
	var text []string
	for _, ct := range res.Content {
		text = append(text, ct.Text)
	}
	return toolResult{strings.Join(text, ""), res.IsError}
}

// awaitListChanged waits for notifications/tools/list_changed on outbound.
func (c *rawClient) awaitListChanged() {
	c.t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case m := <-c.outbound:
			var n struct{ Method string }
			if json.Unmarshal(m, &n) == nil && n.Method == "notifications/tools/list_changed" {
				return
			}
		case <-timeout:
			c.t.Fatal("no notifications/tools/list_changed")
		}
	}
}

func TestTunnelTools(t *testing.T) {
	s, err := mcpbridge.New(testTools(), mcpbridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := newRawClient(t, s)
	c.initialize()

	tools := c.tools()
	if len(tools) != 2 {
		t.Fatalf("tools = %v, want echo and fail", tools)
	}
	if got := tools["echo"]; got.Description != "Echoes text." || !jsonEqual(got.InputSchema, echoSchema) {
		t.Errorf("echo is listed as %+v", got)
	}
	if got := tools["fail"].InputSchema; !jsonEqual(got, json.RawMessage(`{"type":"object","properties":{}}`)) {
		t.Errorf("fail's schema = %s, want an empty object schema", got)
	}

	if got := c.callTool("echo", map[string]any{"text": "hi"}); got != (toolResult{"echo: hi", false}) {
		t.Errorf("echo = %+v", got)
	}
	if got := c.callTool("fail", nil); !got.isError || got.text != "the tool broke" {
		t.Errorf("fail = %+v, want the handler's error", got)
	}
	if got := c.callTool("echo", map[string]any{"text": 3}); !got.isError || !strings.Contains(got.text, "schema") {
		t.Errorf("echo with a number = %+v, want a validation error", got)
	}
	if _, err := c.request("tools/call", map[string]any{"name": "missing"}); err == nil {
		t.Error("a call to an unknown tool succeeded")
	}
}

func TestTunnelRespond(t *testing.T) {
	var rec recorder
	s, err := mcpbridge.New(testTools(), rec.options())
	if err != nil {
		t.Fatal(err)
	}
	c := newRawClient(t, s)
	c.initialize()

	if err := s.SetSchema(answerSchema); err != nil {
		t.Fatal(err)
	}
	c.awaitListChanged()
	name := s.RespondName()
	tools := c.tools()
	respond, ok := tools[name]
	if !ok || len(tools) != 3 {
		t.Fatalf("tools = %v, want echo, fail, and %s", slices.Collect(maps.Keys(tools)), name)
	}
	if !jsonEqual(respond.InputSchema, answerSchema) || respond.Description == "" {
		t.Errorf("%s is listed as %+v", name, respond)
	}

	got := c.callTool(name, map[string]any{"answer": "forty-two"})
	if !got.isError || !strings.Contains(got.text, "schema") {
		t.Errorf("an invalid response = %+v, want a validation error", got)
	}
	if s, r := rec.counts(); s != 0 || r != 1 {
		t.Errorf("after an invalid response: %d structured, %d rejected; want 0, 1", s, r)
	}
	if got := c.callTool(name, map[string]any{"answer": 42}); got.isError {
		t.Errorf("a valid response = %+v", got)
	}
	if s, r := rec.counts(); s != 1 || r != 1 {
		t.Errorf("after a valid response: %d structured, %d rejected; want 1, 1", s, r)
	}
	rec.mu.Lock()
	if v := rec.structured[0]; !jsonEqual(v, json.RawMessage(`{"answer":42}`)) {
		t.Errorf("structured = %s", v)
	}
	rec.mu.Unlock()

	// A new schema renames respond, and a call to the old name is a failed call that names
	// the new one.
	if err := s.SetSchema(answerSchema2); err != nil {
		t.Fatal(err)
	}
	c.awaitListChanged()
	renamed := s.RespondName()
	tools = c.tools()
	if _, old := tools[name]; old {
		t.Errorf("%s is still listed after the schema changed", name)
	}
	if got := tools[renamed]; !jsonEqual(got.InputSchema, answerSchema2) {
		t.Errorf("%s is listed as %+v", renamed, got)
	}
	got = c.callTool(name, map[string]any{"answer": 42})
	if !got.isError || !strings.Contains(got.text, renamed) {
		t.Errorf("a call to the old name = %+v, want a failure naming %s", got, renamed)
	}
	if err := rec.lastRejected(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("OnRejected got %v for the stale call", err)
	}
	if got := c.callTool(renamed, map[string]any{"verdict": "yes"}); got.isError {
		t.Errorf("a valid response to the new schema = %+v", got)
	}

	if err := s.SetSchema(nil); err != nil {
		t.Fatal(err)
	}
	c.awaitListChanged()
	if tools := c.tools(); len(tools) != 2 {
		t.Errorf("tools after removal = %v", slices.Collect(maps.Keys(tools)))
	}
	if got := c.callTool(renamed, map[string]any{"verdict": "yes"}); !got.isError {
		t.Errorf("a response with no schema set = %+v, want a failure", got)
	}
	if s, r := rec.counts(); s != 2 || r != 3 {
		t.Errorf("at the end: %d structured, %d rejected; want 2, 3", s, r)
	}
}

// TestTunnelLateSchema checks that a tunnel opened after SetSchema lists respond.
func TestTunnelLateSchema(t *testing.T) {
	s, err := mcpbridge.New(nil, mcpbridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSchema(answerSchema); err != nil {
		t.Fatal(err)
	}
	c := newRawClient(t, s)
	c.initialize()
	if _, ok := c.tools()[s.RespondName()]; !ok {
		t.Fatal("respond isn't listed")
	}
}

func TestTunnelDeliver(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	tools := append(testTools(), harnessTool("block", func(ctx context.Context, _ json.RawMessage) (string, error) {
		close(started)
		<-ctx.Done()
		close(block)
		return "", ctx.Err()
	}))
	s, err := mcpbridge.New(tools, mcpbridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := newRawClient(t, s)
	c.initialize()

	if _, err := c.tunnel.Deliver(t.Context(), json.RawMessage(`not json`)); err == nil {
		t.Error("Deliver took a message that isn't JSON")
	}

	// A request whose context ends returns, and its handler is cancelled.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := c.tunnel.Deliver(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":"slow","method":"tools/call","params":{"name":"block"}}`))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(wait):
		t.Fatal("the handler didn't start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Deliver = %v, want context.Canceled", err)
		}
	case <-time.After(wait):
		t.Fatal("Deliver didn't return when its context ended")
	}
	select {
	case <-block:
	case <-time.After(wait):
		t.Fatal("the handler wasn't cancelled")
	}

	if err := c.tunnel.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.tunnel.Deliver(t.Context(), json.RawMessage(`{"jsonrpc":"2.0","id":99,"method":"ping"}`)); !errors.Is(err, mcpbridge.ErrTunnelClosed) {
		t.Errorf("Deliver after Close = %v, want ErrTunnelClosed", err)
	}
}

// TestTunnelClient runs go-sdk's own client over a tunnel, at the latest protocol version and
// at 2025-11-25, and checks that each learns of a tool list change.
func TestTunnelClient(t *testing.T) {
	for _, version := range []string{"", "2025-11-25"} {
		t.Run("version="+version, func(t *testing.T) {
			s, err := mcpbridge.New(testTools(), mcpbridge.Options{})
			if err != nil {
				t.Fatal(err)
			}
			transport := newTunnelTransport(t, s)
			changed := make(chan struct{}, 8)
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
				ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
			})
			cs, err := client.Connect(t.Context(), transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cs.Close() })
			exerciseClient(t, s, cs, changed)
		})
	}
}

// exerciseClient lists and calls the tools of testTools through cs, then sets a schema and
// waits for changed and for respond to be listed.
func exerciseClient(t *testing.T, s *mcpbridge.Server, cs *mcp.ClientSession, changed <-chan struct{}) {
	t.Helper()
	ctx := t.Context()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 2 {
		t.Fatalf("listed %d tools, want 2", len(list.Tools))
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; res.IsError || text != "echo: hi" {
		t.Errorf("echo = %q, isError %v", text, res.IsError)
	}

	if err := s.SetSchema(answerSchema); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(wait):
		t.Fatal("the client wasn't told the tool list changed")
	}
	list, err = cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(list.Tools, func(tl *mcp.Tool) bool { return tl.Name == s.RespondName() }) {
		t.Errorf("respond isn't listed after SetSchema")
	}
}

// tunnelTransport is a go-sdk client Transport over a Tunnel, relaying each message as a
// harness would.
type tunnelTransport struct {
	tunnel *mcpbridge.Tunnel
	in     chan json.RawMessage
	closed chan struct{}
	once   sync.Once
}

func newTunnelTransport(t *testing.T, s *mcpbridge.Server) *tunnelTransport {
	tt := &tunnelTransport{in: make(chan json.RawMessage), closed: make(chan struct{})}
	tunnel, err := s.Tunnel(t.Context(), mcpbridge.WithOutbound(tt.receive))
	if err != nil {
		t.Fatal(err)
	}
	tt.tunnel = tunnel
	t.Cleanup(func() { _ = tunnel.Close() })
	return tt
}

func (tt *tunnelTransport) receive(m json.RawMessage) {
	select {
	case tt.in <- m:
	case <-tt.closed:
	}
}

func (tt *tunnelTransport) Connect(context.Context) (mcp.Connection, error) { return tt, nil }

func (tt *tunnelTransport) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case m := <-tt.in:
		return jsonrpc.DecodeMessage(m)
	case <-tt.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (tt *tunnelTransport) Write(ctx context.Context, m jsonrpc.Message) error {
	raw, err := jsonrpc.EncodeMessage(m)
	if err != nil {
		return err
	}
	if req, ok := m.(*jsonrpc.Request); ok && req.IsCall() {
		// A request may stay open, as subscriptions/listen does, so it waits on its own
		// goroutine, until the tunnel closes.
		go func() {
			resp, err := tt.tunnel.Deliver(context.Background(), raw)
			if err == nil {
				tt.receive(resp)
			}
		}()
		return nil
	}
	_, err = tt.tunnel.Deliver(ctx, raw)
	return err
}

func (tt *tunnelTransport) Close() error {
	tt.once.Do(func() { close(tt.closed) })
	return nil
}

func (tt *tunnelTransport) SessionID() string { return "" }

func harnessTool(name string, h func(context.Context, json.RawMessage) (string, error)) harness.Tool {
	return harness.Tool{Name: name, Description: name, Handler: h}
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

func TestWaitListed(t *testing.T) {
	s, err := mcpbridge.New(nil, mcpbridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	waited := func() time.Duration {
		start := time.Now()
		if err := s.WaitListed(t.Context(), 300*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}

	// Before any harness has listed the tools, a change needs no wait.
	if err := s.SetSchema(json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	if d := waited(); d > 100*time.Millisecond {
		t.Fatalf("waited %s with no listing yet", d)
	}

	c := newRawClient(t, s)
	c.initialize()
	c.tools()
	if d := waited(); d > 100*time.Millisecond {
		t.Fatalf("waited %s after a current listing", d)
	}

	// A change waits for the next listing, and gives up at max without one.
	if err := s.SetSchema(json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)); err != nil {
		t.Fatal(err)
	}
	if d := waited(); d < 250*time.Millisecond {
		t.Fatalf("returned after %s with no listing since the change", d)
	}
	listed := make(chan struct{})
	go func() {
		defer close(listed)
		time.Sleep(50 * time.Millisecond)
		c.tools()
	}()
	if d := waited(); d > 250*time.Millisecond {
		t.Fatalf("waited %s, past the listing", d)
	}
	<-listed
}
