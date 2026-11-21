package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

const fakeEnv = "CLAUDE_FAKE"

const noRaceExitSleep = "GORACE=atexit_sleep_ms=0"

const (
	launchEnv  = "CLAUDE_FAKE_LAUNCH"
	promptsEnv = "CLAUDE_FAKE_PROMPTS"
	configEnv  = "CLAUDE_FAKE_CONFIG"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) != "" {
		os.Exit(fakeClaude())
	}
	os.Exit(m.Run())
}

type fake struct {
	out         sync.Mutex
	mu          sync.Mutex
	next        int
	waiting     map[string]chan json.RawMessage
	tools       []string
	connected   bool
	listing     sync.WaitGroup
	mcpID       int
	interrupted chan struct{}
	userBlocks  chan []json.RawMessage
	failed      bool
	done        chan struct{}
}

func fakeClaude() int {
	f := &fake{waiting: map[string]chan json.RawMessage{}, userBlocks: make(chan []json.RawMessage, 8), done: make(chan struct{})}
	record(launchEnv, os.Args[1:])
	record(configEnv, os.Getenv("CLAUDE_CONFIG_DIR"))
	go f.runTurns()
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(nil, 16<<20)
	for in.Scan() {
		var l struct {
			Type      string          `json:"type"`
			RequestID string          `json:"request_id"`
			Request   controlRequest  `json:"request"`
			Response  controlReply    `json:"response"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal(in.Bytes(), &l) != nil {
			return 2
		}
		switch l.Type {
		case "control_response":
			f.mu.Lock()
			ch := f.waiting[l.Response.RequestID]
			delete(f.waiting, l.Response.RequestID)
			f.mu.Unlock()
			if ch != nil {
				ch <- l.Response.Response
			}
		case "control_request":
			f.control(l.RequestID, l.Request)
		case "user":
			var m struct {
				Content []json.RawMessage `json:"content"`
			}
			_ = json.Unmarshal(l.Message, &m)
			record(promptsEnv, m.Content)
			f.userBlocks <- m.Content
		}
	}
	close(f.userBlocks)
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed {
		return 1
	}
	return 0
}

func record(env string, v any) {
	path := os.Getenv(env)
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

func (f *fake) reply(id string, response any) {
	b, _ := json.Marshal(response)
	f.emit(controlResponse{Type: "control_response", Response: controlReply{Subtype: "success", RequestID: id, Response: b}})
}

func (f *fake) control(id string, r controlRequest) {
	switch r.Subtype {
	case "initialize":
		f.reply(id, map[string]any{"commands": []any{}})
	case "interrupt":
		f.reply(id, map[string]any{"still_queued": []any{}})
		f.mu.Lock()
		if f.interrupted != nil {
			close(f.interrupted)
			f.interrupted = nil
		}
		f.mu.Unlock()
	case "mcp_message":
		f.reply(id, nil)
		var m struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(r.Message, &m)
		if m.Method == "notifications/tools/list_changed" {
			f.listing.Go(f.list)
		}
	default:
		f.emit(controlResponse{Type: "control_response", Response: controlReply{Subtype: "error", RequestID: id, Error: "unsupported"}})
	}
}

func (f *fake) ask(r controlRequest) json.RawMessage {
	f.mu.Lock()
	f.next++
	id := fmt.Sprintf("fake-%d", f.next)
	ch := make(chan json.RawMessage, 1)
	f.waiting[id] = ch
	f.mu.Unlock()
	req, _ := json.Marshal(r)
	f.emit(controlEnvelope{Type: "control_request", RequestID: id, Request: req})
	return <-ch
}

func (f *fake) mcp(msg string) json.RawMessage {
	var out struct {
		MCPResponse json.RawMessage `json:"mcp_response"`
	}
	_ = json.Unmarshal(f.ask(controlRequest{Subtype: "mcp_message", ServerName: serverName, Message: json.RawMessage(msg)}), &out)
	return out.MCPResponse
}

func (f *fake) nextMCPID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpID++
	return fmt.Sprint(f.mcpID)
}

func (f *fake) connect() {
	f.mcp(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"fake","version":"0"}}}`)
	f.mcp(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	f.list()
	f.mu.Lock()
	f.connected = true
	f.mu.Unlock()
}

func (f *fake) list() {
	var r struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(f.mcp(`{"jsonrpc":"2.0","id":`+f.nextMCPID()+`,"method":"tools/list"}`), &r)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = f.tools[:0]
	for _, t := range r.Result.Tools {
		f.tools = append(f.tools, t.Name)
	}
}

func (f *fake) call(name string, args json.RawMessage) (string, bool) {
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(f.nextMCPID()), "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	var r struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(f.mcp(string(msg)), &r)
	text := ""
	for _, c := range r.Result.Content {
		text += c.Text
	}
	return text, r.Result.IsError
}

func (f *fake) runTurns() {
	defer close(f.done)
	for content := range f.userBlocks {
		f.listing.Wait()
		f.mu.Lock()
		connected := f.connected
		f.mu.Unlock()
		if !connected {
			f.connect()
		}
		text := ""
		for _, b := range content {
			var blk contentBlock
			_ = json.Unmarshal(b, &blk)
			if blk.Type == "text" {
				text = blk.Text
			}
		}
		text, _, _ = strings.Cut(text, "\n\n")
		f.emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s"})
		switch {
		case strings.HasPrefix(text, "tool:"):
			name, args, _ := strings.Cut(strings.TrimPrefix(text, "tool:"), ":")
			f.toolTurn(toolPrefix+name, name, json.RawMessage(args))
		case strings.HasPrefix(text, "structured:"):
			f.mu.Lock()
			name := ""
			for _, t := range f.tools {
				if strings.HasPrefix(t, "respond_") {
					name = t
				}
			}
			f.mu.Unlock()
			f.toolTurn(toolPrefix+name, name, json.RawMessage(strings.TrimPrefix(text, "structured:")))
		case strings.HasPrefix(text, "permit:"):
			tool := strings.TrimPrefix(text, "permit:")
			var d struct {
				Behavior string `json:"behavior"`
			}
			_ = json.Unmarshal(f.ask(controlRequest{Subtype: "can_use_tool", ToolName: tool, Input: json.RawMessage(`{"x":1}`)}), &d)
			f.end(d.Behavior, "")
		case text == "stream":
			f.mu.Lock()
			interrupted := make(chan struct{})
			f.interrupted = interrupted
			f.mu.Unlock()
			for {
				select {
				case <-interrupted:
					f.end("", "aborted_streaming")
					goto next
				case <-time.After(10 * time.Millisecond):
					f.delta("tick ")
				}
			}
		default:
			f.delta("hel")
			f.delta("lo")
			f.end("hello", "")
		}
	next:
	}
}

func (f *fake) delta(text string) {
	f.emit(map[string]any{"type": "stream_event", "event": map[string]any{
		"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": text}}})
}

func (f *fake) toolTurn(wire, name string, args json.RawMessage) {
	f.emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_use", "id": "t1", "name": wire, "input": args}}}})
	text, failed := f.call(name, args)
	f.emit(map[string]any{"type": "user", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "t1", "is_error": failed, "content": text}}}})
	f.end(text, "")
}

func (f *fake) end(text, terminal string) {
	r := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text,
		"stop_reason": "end_turn", "terminal_reason": "completed",
		"usage": map[string]int{"input_tokens": 3, "output_tokens": 2, "cache_read_input_tokens": 5}}
	if terminal != "" {
		r["subtype"], r["is_error"], r["stop_reason"], r["terminal_reason"] = "error_during_execution", true, nil, terminal
	}
	f.mu.Lock()
	f.failed = terminal != ""
	f.mu.Unlock()
	f.emit(r)
}

func newBridgeForTest() (*mcpbridge.Server, error) {
	return mcpbridge.New(nil, mcpbridge.Options{Name: serverName})
}
