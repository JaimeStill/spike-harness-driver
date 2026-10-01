package opencode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// replay decodes a testdata transcript with one codec, as the Client's reader would, and
// returns each turn's events. The transcript holds OpenCode's side only, so replay encodes a
// session/prompt under each id that answers with a stop reason, before the turn it ends.
func replay(t *testing.T, name string) [][]harness.Event {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var prompts []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Result struct {
				StopReason string `json:"stopReason"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Result.StopReason != "" {
			prompts = append(prompts, string(m.ID))
		}
	}
	c := newCodec()
	prompt := func() {
		if len(prompts) > 0 {
			if _, err := c.Encode(prompts[0], call{Method: "session/prompt"}); err != nil {
				t.Fatal(err)
			}
			prompts = prompts[1:]
		}
	}
	var turns [][]harness.Event
	var turn []harness.Event
	sc = bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	handshake := true
	for sc.Scan() {
		line := slices.Clone(sc.Bytes())
		fr, err := c.Decode(line)
		if err != nil {
			t.Fatal(err)
		}
		// The handshake ends with the model's selection, answered as id 3.
		if handshake && fr.Response != nil && fr.Response.ID == "3" {
			handshake = false
			prompt()
			continue
		}
		turn = append(turn, fr.Events...)
		if slices.ContainsFunc(fr.Events, func(ev harness.Event) bool { return ev.Kind == harness.EventEnded }) {
			turns, turn = append(turns, turn), nil
			prompt()
		}
	}
	return turns
}

// normalized drops EventHarness and merges each run of deltas of one kind.
func normalized(events []harness.Event) []harness.EventKind {
	var kinds []harness.EventKind
	for _, ev := range events {
		if ev.Kind == harness.EventHarness {
			continue
		}
		if n := len(kinds); n > 0 && kinds[n-1] == ev.Kind && (ev.Kind == harness.EventTextDelta || ev.Kind == harness.EventThinkingDelta) {
			continue
		}
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func find(events []harness.Event, kind harness.EventKind) []harness.Event {
	var out []harness.Event
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestToolsAndStructuredTurns(t *testing.T) {
	turns := replay(t, "tools.jsonl")
	if len(turns) != 3 {
		t.Fatalf("%d turns, want 3", len(turns))
	}

	tool := turns[0]
	if k := normalized(tool); k[0] != harness.EventStarted || k[len(k)-2] != harness.EventMessageEnd || k[len(k)-1] != harness.EventEnded {
		t.Fatalf("turn 1 kinds = %v", k)
	}
	call, result := find(tool, harness.EventToolCall), find(tool, harness.EventToolResult)
	if len(call) != 1 || call[0].Tool.Name != "echo" || string(call[0].Tool.Args) != `{"text":"seven"}` {
		t.Fatalf("calls = %+v, want echo once, with its arguments, without the server's prefix", call)
	}
	if len(result) != 1 || result[0].Tool.Name != "echo" || result[0].Tool.CallID != call[0].Tool.CallID || result[0].Tool.IsError {
		t.Fatalf("results = %+v", result)
	}
	end := find(tool, harness.EventMessageEnd)[0]
	if end.StopReason != "end_turn" || end.Usage == nil || *end.Usage != (harness.Usage{Input: 39, Output: 20, CacheRead: 2521}) || end.Text == "" {
		t.Errorf("end = stop %q usage %+v text %q", end.StopReason, end.Usage, end.Text)
	}

	for i, want := range []string{`{"city":"Paris","country":"France"}`, `{"count":3,"colors":["red","yellow","blue"]}`} {
		events := turns[i+1]
		s := find(events, harness.EventStructured)
		if len(s) != 1 || string(s[0].Structured) != want {
			t.Errorf("turn %d structured = %+v, want %s", i+2, s, want)
		}
		for _, ev := range events {
			if ev.Tool != nil && ev.Tool.Name != "echo" {
				t.Errorf("turn %d: respond surfaced as a tool event: %+v", i+2, ev.Tool)
			}
		}
	}
}

func TestACancelledTurn(t *testing.T) {
	turns := replay(t, "cancel.jsonl")
	if len(turns) != 2 {
		t.Fatalf("%d turns, want 2", len(turns))
	}
	k := normalized(turns[0])
	if !slices.Equal(k[len(k)-3:], []harness.EventKind{harness.EventMessageEnd, harness.EventCancelled, harness.EventEnded}) {
		t.Fatalf("cancelled turn kinds = %v", k)
	}
	if end := find(turns[0], harness.EventMessageEnd)[0]; end.StopReason != "aborted" || end.Text == "" {
		t.Errorf("end = %q %q, want aborted with the text so far", end.StopReason, end.Text)
	}
	if end := find(turns[1], harness.EventMessageEnd)[0]; end.StopReason != "end_turn" || end.Text != "OK" {
		t.Errorf("next turn = %q %q", end.StopReason, end.Text)
	}
}

// decodeAll decodes lines with c and returns their events.
func decodeAll(t *testing.T, c *codec, lines ...string) []harness.Event {
	t.Helper()
	var events []harness.Event
	for _, l := range lines {
		fr, err := c.Decode([]byte(l))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, fr.Events...)
	}
	return events
}

func TestALoadsReplayBelongsToNoExchange(t *testing.T) {
	c := newCodec()
	if _, err := c.Encode("2", call{Method: "session/load"}); err != nil {
		t.Fatal(err)
	}
	events := decodeAll(t, c,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","messageId":"m","content":{"type":"text","text":"hello"}}}}`,
		`{"jsonrpc":"2.0","id":2,"result":{}}`,
	)
	if k := normalized(events); len(k) != 0 {
		t.Fatalf("replayed history = %v, want only harness events", k)
	}
}

func TestAFailedPromptIsTheExchangesError(t *testing.T) {
	c := newCodec()
	if _, err := c.Encode("4", call{Method: "session/prompt"}); err != nil {
		t.Fatal(err)
	}
	events := decodeAll(t, c, `{"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"Internal error","data":{"service":"provider"}}}`)
	k := normalized(events)
	if !slices.Equal(k, []harness.EventKind{harness.EventStarted, harness.EventMessageEnd, harness.EventError, harness.EventEnded}) {
		t.Fatalf("kinds = %v", k)
	}
	if err := find(events, harness.EventError)[0].Err; err == nil || err.Error() != `opencode: Internal error (-32603): {"service":"provider"}` {
		t.Errorf("error = %v", err)
	}
}

// A respond call is the structured response only when it completed with the bridge's
// acceptance; a failed call, or one whose output is anything else, is a rejection.
func TestARespondCallsVerdict(t *testing.T) {
	upd := func(u string) string {
		return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":` + u + `}}`
	}
	for _, tt := range []struct {
		name, end string
		want      harness.EventKind
		err       string
	}{
		{name: "failed", end: `"status":"failed","rawOutput":{"error":"missing properties: [\"city\"]"}`,
			want: harness.EventStructuredRejected, err: `opencode: respond: missing properties: ["city"]`},
		{name: "completed without the acceptance", end: `"status":"completed","rawOutput":{"output":"the response schema changed"}`,
			want: harness.EventStructuredRejected, err: `opencode: respond: the response schema changed`},
		{name: "accepted", end: `"status":"completed","rawOutput":{"output":"Accepted.","metadata":{"truncated":false}}`,
			want: harness.EventStructured},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newCodec()
			if _, err := c.Encode("4", call{Method: "session/prompt"}); err != nil {
				t.Fatal(err)
			}
			events := decodeAll(t, c,
				upd(`{"sessionUpdate":"tool_call","toolCallId":"r1","title":"driver_respond_0f3a9c21","status":"pending","rawInput":{}}`),
				upd(`{"sessionUpdate":"tool_call_update","toolCallId":"r1","title":"driver_respond_0f3a9c21","status":"in_progress","rawInput":{"city":"Paris"}}`),
				upd(`{"sessionUpdate":"tool_call_update","toolCallId":"r1",`+tt.end+`}`),
			)
			if k := normalized(events); !slices.Equal(k, []harness.EventKind{harness.EventStarted, tt.want}) {
				t.Fatalf("kinds = %v", k)
			}
			ev := find(events, tt.want)[0]
			switch {
			case tt.err != "" && (ev.Err == nil || ev.Err.Error() != tt.err):
				t.Errorf("rejection = %v, want %s", ev.Err, tt.err)
			case tt.err == "" && string(ev.Structured) != `{"city":"Paris"}`:
				t.Errorf("structured = %s", ev.Structured)
			}
		})
	}
}

// A tool that takes no arguments is still reported as called: its rawInput stays {}.
func TestACallWithNoArguments(t *testing.T) {
	c := newCodec()
	if _, err := c.Encode("4", call{Method: "session/prompt"}); err != nil {
		t.Fatal(err)
	}
	upd := func(u string) string {
		return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":` + u + `}}`
	}
	events := decodeAll(t, c,
		upd(`{"sessionUpdate":"tool_call","toolCallId":"c1","title":"driver_transcribe","status":"pending","rawInput":{}}`),
		upd(`{"sessionUpdate":"tool_call_update","toolCallId":"c1","title":"driver_transcribe","status":"in_progress","rawInput":{}}`),
		upd(`{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed","rawOutput":{"output":"7429"}}`),
	)
	k := normalized(events)
	if !slices.Equal(k, []harness.EventKind{harness.EventStarted, harness.EventToolCall, harness.EventToolResult}) {
		t.Fatalf("kinds = %v", k)
	}
	if call := find(events, harness.EventToolCall)[0]; call.Tool.Name != "transcribe" || string(call.Tool.Args) != "{}" {
		t.Errorf("call = %+v", call.Tool)
	}
}

func TestMessages(t *testing.T) {
	c := newCodec()
	line, err := c.Encode("7", call{Method: "session/new", Params: map[string]string{"cwd": "/w"}})
	if err != nil || string(line) != `{"id":7,"jsonrpc":"2.0","method":"session/new","params":{"cwd":"/w"}}` {
		t.Fatalf("Encode = %s, %v", line, err)
	}
	line, _ = c.Encode("", call{Method: "session/cancel", Params: map[string]string{"sessionId": "s"}})
	if string(line) != `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s"}}` {
		t.Fatalf("notification = %s", line)
	}

	fr, err := c.Decode([]byte(`{"jsonrpc":"2.0","id":"perm-1","method":"session/request_permission","params":{"toolCall":{"title":"bash"}}}`))
	if err != nil || fr.Request == nil || fr.Request.ID != `"perm-1"` {
		t.Fatalf("request = %+v, %v", fr.Request, err)
	}
	reply, _ := c.Reply(*fr.Request, map[string]string{"ok": "1"})
	if string(reply) != `{"id":"perm-1","jsonrpc":"2.0","result":{"ok":"1"}}` {
		t.Fatalf("Reply = %s, want the id echoed as it came", reply)
	}
	reply, _ = c.Reply(stdio.Request{ID: strconv.Itoa(9)}, errors.New("nope"))
	if string(reply) != `{"error":{"code":-32603,"message":"nope"},"id":9,"jsonrpc":"2.0"}` {
		t.Fatalf("error Reply = %s", reply)
	}
}
