package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeEnv switches the test binary into a fake OpenCode, which speaks enough ACP to drive the
// connection: the handshake, sessions it persists under XDG_DATA_HOME, turns that end with
// session/prompt's answer, cancellation, permission requests, and the driver's tools, which it
// calls over HTTP with go-sdk's MCP client as OpenCode calls them.
const fakeEnv = "OPENCODE_FAKE"

// launchEnv names a file where the fake records its arguments and configuration.
const launchEnv = "OPENCODE_FAKE_LAUNCH"

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) != "" {
		os.Exit(fakeOpenCode())
	}
	os.Exit(m.Run())
}

type fake struct {
	out sync.Mutex
	mu  sync.Mutex
	// mcp is the session's client of the driver's tool server, and tools its last listing.
	mcp   *mcp.ClientSession
	tools []string
	// listing counts listings under way, which a turn waits for, as OpenCode applies a
	// changed tool list before it runs the next prompt.
	listing sync.WaitGroup
	// cancelled is closed by session/cancel to cancel the turn under way, and is nil while
	// none is. A turn is under way from the moment its session/prompt is read, so a cancel
	// read after it cancels it, and a cancel read while the session is idle is ignored, as
	// OpenCode ignores it.
	cancelled chan struct{}
	next      int
	waiting   map[string]chan json.RawMessage
	prompts   sync.WaitGroup
}

// rpcIn is a message the fake reads.
type rpcIn struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func fakeOpenCode() int {
	f := &fake{waiting: map[string]chan json.RawMessage{}}
	record(map[string]any{"args": os.Args[1:], "config": json.RawMessage(os.Getenv("OPENCODE_CONFIG_CONTENT"))})
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(nil, 16<<20)
	for in.Scan() {
		var m rpcIn
		if json.Unmarshal(in.Bytes(), &m) != nil {
			return 2
		}
		switch {
		case m.Method == "" && len(m.ID) > 0:
			f.mu.Lock()
			ch := f.waiting[string(m.ID)]
			f.mu.Unlock()
			if ch != nil {
				ch <- m.Result
			}
		case m.Method == "session/cancel":
			f.mu.Lock()
			if f.cancelled != nil {
				close(f.cancelled)
				f.cancelled = nil
			}
			f.mu.Unlock()
		case m.Method == "session/prompt":
			cancelled := make(chan struct{})
			f.mu.Lock()
			f.cancelled = cancelled
			f.mu.Unlock()
			f.prompts.Go(func() { f.prompt(m, cancelled) })
		default:
			f.handle(m)
		}
	}
	f.prompts.Wait()
	return 0
}

func record(v any) {
	path := os.Getenv(launchEnv)
	if path == "" {
		return
	}
	b, _ := json.Marshal(v)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(append(b, '\n'))
}

func (f *fake) emit(v any) {
	b, _ := json.Marshal(v)
	f.out.Lock()
	defer f.out.Unlock()
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func (f *fake) result(id json.RawMessage, result any) {
	f.emit(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fake) fail(id json.RawMessage, msg string) {
	f.emit(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": msg}})
}

func (f *fake) update(u map[string]any) {
	f.emit(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s", "update": u}})
}

// sessions is the file where the fake keeps the IDs of the sessions it created.
func sessions() string { return filepath.Join(os.Getenv("XDG_DATA_HOME"), "sessions") }

func (f *fake) handle(m rpcIn) {
	switch m.Method {
	case "initialize":
		f.result(m.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}})
	case "session/new", "session/load":
		var p struct {
			SessionID  string      `json:"sessionId"`
			MCPServers []mcpServer `json:"mcpServers"`
		}
		_ = json.Unmarshal(m.Params, &p)
		data, _ := os.ReadFile(sessions())
		if m.Method == "session/load" && !slices.Contains(strings.Fields(string(data)), p.SessionID) {
			f.fail(m.ID, "Internal error: OpenCode service failure")
			return
		}
		if err := f.connect(p.MCPServers); err != nil {
			f.fail(m.ID, err.Error())
			return
		}
		if m.Method == "session/load" {
			// The history replays before the answer.
			f.update(map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "earlier"}})
			f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m0", "content": map[string]any{"type": "text", "text": "reply"}})
			f.result(m.ID, map[string]any{})
			return
		}
		id := fmt.Sprintf("ses_fake%d", time.Now().UnixNano())
		_ = os.MkdirAll(filepath.Dir(sessions()), 0o700)
		_ = os.WriteFile(sessions(), append(data, []byte(id+"\n")...), 0o600)
		f.result(m.ID, map[string]any{"sessionId": id})
	case "session/set_config_option":
		f.result(m.ID, map[string]any{})
	default:
		f.fail(m.ID, "unsupported")
	}
}

// bearer adds the server's Authorization header to every request.
type bearer struct {
	value string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", b.value)
	return b.next.RoundTrip(r)
}

// connect connects to the driver's MCP server over HTTP, as OpenCode does at session/new.
func (f *fake) connect(servers []mcpServer) error {
	if len(servers) != 1 || servers[0].Type != "http" || len(servers[0].Headers) != 1 {
		return fmt.Errorf("want one http MCP server with one header, got %+v", servers)
	}
	s := servers[0]
	client := mcp.NewClient(&mcp.Implementation{Name: "fake-opencode", Version: "1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { f.listing.Go(f.list) },
	})
	transport := &mcp.StreamableClientTransport{Endpoint: s.URL,
		HTTPClient: &http.Client{Transport: bearer{s.Headers[0].Value, http.DefaultTransport}}}
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.mcp = cs
	f.mu.Unlock()
	f.list()
	return nil
}

func (f *fake) list() {
	f.mu.Lock()
	cs := f.mcp
	f.mu.Unlock()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = f.tools[:0]
	for _, t := range res.Tools {
		f.tools = append(f.tools, t.Name)
	}
}

// ask sends the driver a request and waits for its result.
func (f *fake) ask(method string, params any) json.RawMessage {
	f.mu.Lock()
	f.next++
	id := fmt.Sprintf(`"fake-%d"`, f.next)
	ch := make(chan json.RawMessage, 1)
	f.waiting[id] = ch
	f.mu.Unlock()
	f.emit(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
	return <-ch
}

// prompt runs one turn. The prompt's text decides what it does:
//
//   - "tool:<name>:<args>" calls the driver's tool with the arguments;
//   - "structured:<args>" calls the respond tool the fake last listed with the arguments;
//   - "permit:<title>" asks the driver whether a call titled so may run, and answers with the
//     option;
//   - "image" answers with the media types of the prompt's images;
//   - "stream" streams text until cancelled;
//   - anything else answers "hello".
//
// cancelled closes when session/cancel cancels the turn.
func (f *fake) prompt(m rpcIn, cancelled chan struct{}) {
	defer func() {
		f.mu.Lock()
		if f.cancelled == cancelled {
			f.cancelled = nil
		}
		f.mu.Unlock()
	}()
	f.listing.Wait()
	var p struct {
		Prompt []contentBlock `json:"prompt"`
	}
	_ = json.Unmarshal(m.Params, &p)
	text := ""
	var images []string
	for _, b := range p.Prompt {
		switch b.Type {
		case "text":
			text = b.Text
		case "image":
			images = append(images, b.MimeType+":"+b.Data)
		}
	}
	text, _, _ = strings.Cut(text, "\n\n")
	chunk := func(s string) {
		f.update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": s}})
	}
	usage := map[string]int{"inputTokens": 3, "outputTokens": 2, "cachedReadTokens": 5}
	end := func(stop string) { f.result(m.ID, map[string]any{"stopReason": stop, "usage": usage}) }
	switch {
	case strings.HasPrefix(text, "tool:"):
		name, args, _ := strings.Cut(strings.TrimPrefix(text, "tool:"), ":")
		chunk(f.call(name, json.RawMessage(args)))
		end("end_turn")
	case strings.HasPrefix(text, "structured:"):
		f.mu.Lock()
		name := ""
		for _, t := range f.tools {
			if strings.HasPrefix(t, "respond_") {
				name = t
			}
		}
		f.mu.Unlock()
		chunk(f.call(name, json.RawMessage(strings.TrimPrefix(text, "structured:"))))
		end("end_turn")
	case strings.HasPrefix(text, "permit:"):
		var r struct {
			Outcome struct {
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(f.ask("session/request_permission", map[string]any{
			"sessionId": "s", "toolCall": map[string]any{"toolCallId": "p1", "title": strings.TrimPrefix(text, "permit:")},
			"options": []map[string]string{{"optionId": "once", "kind": "allow_once"}, {"optionId": "reject", "kind": "reject_once"}},
		}), &r)
		chunk(r.Outcome.OptionID)
		end("end_turn")
	case text == "image":
		chunk(strings.Join(images, ","))
		end("end_turn")
	case text == "stream":
		for {
			select {
			case <-cancelled:
				usage = map[string]int{}
				end("cancelled")
				return
			case <-time.After(10 * time.Millisecond):
				chunk("tick ")
			}
		}
	default:
		chunk("hel")
		chunk("lo")
		end("end_turn")
	}
}

// call calls a driver tool, reporting the call as OpenCode does: pending with no arguments,
// in progress with them, then completed or failed. It returns the result's text.
func (f *fake) call(name string, args json.RawMessage) string {
	id := fmt.Sprintf("call-%d", time.Now().UnixNano())
	title := toolPrefix + name
	f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": title, "status": "pending", "rawInput": map[string]any{}})
	f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "title": title, "status": "in_progress", "rawInput": args})
	f.mu.Lock()
	cs := f.mcp
	f.mu.Unlock()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	text, status := "", "completed"
	switch {
	case err != nil:
		text, status = err.Error(), "failed"
	default:
		for _, c := range res.Content {
			if t, ok := c.(*mcp.TextContent); ok {
				text += t.Text
			}
		}
		if res.IsError {
			status = "failed"
		}
	}
	out := map[string]any{"output": text}
	if status == "failed" {
		out = map[string]any{"error": text}
	}
	f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status, "rawOutput": out})
	return text
}
