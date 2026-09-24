package pi

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		kinds []harness.EventKind
		text  string
		stop  string
		tool  string
		err   string
	}{
		{name: "agent start", line: `{"type":"agent_start"}`, kinds: []harness.EventKind{harness.EventStarted}},
		{name: "agent settled", line: `{"type":"agent_settled"}`, kinds: []harness.EventKind{harness.EventEnded}},
		{
			name:  "text delta",
			line:  `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hi"}}`,
			kinds: []harness.EventKind{harness.EventTextDelta}, text: "Hi",
		},
		{
			name:  "thinking delta",
			line:  `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"hm"}}`,
			kinds: []harness.EventKind{harness.EventThinkingDelta}, text: "hm",
		},
		{
			name:  "text start is harness noise",
			line:  `{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}`,
			kinds: []harness.EventKind{harness.EventHarness},
		},
		{
			name:  "tool call",
			line:  `{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"ls"}}`,
			kinds: []harness.EventKind{harness.EventToolCall}, tool: "bash",
		},
		{
			name:  "tool result",
			line:  `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[]},"isError":false}`,
			kinds: []harness.EventKind{harness.EventToolResult}, tool: "bash",
		},
		{
			name:  "assistant message end",
			line:  `{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"Go."}],"stopReason":"stop","usage":{"input":3,"output":2}}}`,
			kinds: []harness.EventKind{harness.EventMessageEnd}, text: "Go.", stop: "stop",
		},
		{
			name:  "aborted message end",
			line:  `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"aborted","errorMessage":"Request was aborted"}}`,
			kinds: []harness.EventKind{harness.EventMessageEnd, harness.EventCancelled}, stop: "aborted",
		},
		{
			name:  "errored message end",
			line:  `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"boom"}}`,
			kinds: []harness.EventKind{harness.EventMessageEnd, harness.EventError}, stop: "error", err: "boom",
		},
		{
			name:  "user message end is harness noise",
			line:  `{"type":"message_end","message":{"role":"user","content":"hi"}}`,
			kinds: []harness.EventKind{harness.EventHarness},
		},
		{
			name:  "respond result is the structured response",
			line:  `{"type":"tool_execution_end","toolCallId":"r","toolName":"respond","result":{"content":[],"details":{"a":1},"terminate":true},"isError":false}`,
			kinds: []harness.EventKind{harness.EventToolResult, harness.EventStructured}, tool: "respond",
		},
		{
			name:  "a failed respond call is no structured response",
			line:  `{"type":"tool_execution_end","toolCallId":"r","toolName":"respond","result":{"content":[]},"isError":true}`,
			kinds: []harness.EventKind{harness.EventToolResult}, tool: "respond",
		},
		{
			name:  "extension error",
			line:  `{"type":"extension_error","extensionPath":"/x/bridge.ts","event":"before_agent_start","error":"no answer"}`,
			kinds: []harness.EventKind{harness.EventError}, err: "pi: extension /x/bridge.ts: before_agent_start: no answer",
		},
		{name: "unknown event", line: `{"type":"something_new"}`, kinds: []harness.EventKind{harness.EventHarness}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := decode([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			events := normalize(r, []byte(tt.line))
			if got := kinds(events); !slices.Equal(got, tt.kinds) {
				t.Fatalf("kinds = %v, want %v", got, tt.kinds)
			}
			first, last := events[0], events[len(events)-1]
			if first.Text != tt.text {
				t.Errorf("Text = %q, want %q", first.Text, tt.text)
			}
			if first.StopReason != tt.stop {
				t.Errorf("StopReason = %q, want %q", first.StopReason, tt.stop)
			}
			if tt.tool != "" && (first.Tool == nil || first.Tool.Name != tt.tool) {
				t.Errorf("Tool = %+v, want name %q", first.Tool, tt.tool)
			}
			if got := errText(last.Err); got != tt.err {
				t.Errorf("Err = %q, want %q", got, tt.err)
			}
			if string(first.Raw) != tt.line {
				t.Errorf("Raw = %s, want the input line", first.Raw)
			}
		})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestUsageCountsTheCache(t *testing.T) {
	line := `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"stop","usage":{"input":37,"output":10,"cacheRead":745,"cacheWrite":3}}}`
	r, _ := decode([]byte(line))
	u := normalize(r, []byte(line))[0].Usage
	if u == nil || *u != (harness.Usage{Input: 37, Output: 10, CacheRead: 745, CacheWrite: 3}) {
		t.Fatalf("Usage = %+v", u)
	}
}

func TestStructuredResponse(t *testing.T) {
	line := `{"type":"tool_execution_end","toolCallId":"r","toolName":"respond","result":{"content":[],"details":{"a":1}},"isError":false}`
	r, _ := decode([]byte(line))
	ev := normalize(r, []byte(line))[1]
	if string(ev.Structured) != `{"a":1}` {
		t.Fatalf("Structured = %s", ev.Structured)
	}
}

func TestARespondWithoutDetailsIsNoStructuredResponse(t *testing.T) {
	line := `{"type":"tool_execution_end","toolCallId":"r","toolName":"respond","result":{"content":[]},"isError":false}`
	r, _ := decode([]byte(line))
	events := normalize(r, []byte(line))
	if last := events[len(events)-1]; !errors.Is(last.Err, harness.ErrNoStructuredResponse) {
		t.Fatalf("event = %+v, want ErrNoStructuredResponse", last)
	}
}

func TestDialogs(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		request bool
	}{
		{name: "input", line: `{"type":"extension_ui_request","id":"u1","method":"input","title":"pi-driver:call","placeholder":"{}"}`, request: true},
		{name: "confirm", line: `{"type":"extension_ui_request","id":"u2","method":"confirm","title":"Sure?","message":"m"}`, request: true},
		{name: "notify is fire-and-forget", line: `{"type":"extension_ui_request","id":"u3","method":"notify","message":"hi"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := codec{}.Decode([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			if (f.Request != nil) != tt.request {
				t.Fatalf("Request = %+v, want a request %v", f.Request, tt.request)
			}
			if len(f.Events) != 1 || f.Events[0].Kind != harness.EventHarness {
				t.Fatalf("Events = %+v, want the line as an EventHarness", f.Events)
			}
		})
	}

	req := stdio.Request{ID: "u1"}
	for _, tt := range []struct {
		answer any
		want   string
	}{
		{answer: `{"result":"ok"}`, want: `{"type":"extension_ui_response","id":"u1","value":"{\"result\":\"ok\"}"}`},
		{answer: nil, want: `{"type":"extension_ui_response","id":"u1","cancelled":true}`},
	} {
		line, err := codec{}.Reply(req, tt.answer)
		if err != nil || string(line) != tt.want {
			t.Errorf("Reply(%v) = %s, %v; want %s", tt.answer, line, err, tt.want)
		}
	}
	if _, err := (codec{}).Reply(req, 42); err == nil {
		t.Error("Reply accepted an answer that isn't a dialog's")
	}
}

func TestDecodeRejectsMalformedLine(t *testing.T) {
	if _, err := decode([]byte(`{"type":`)); err == nil {
		t.Fatal("decode accepted a malformed line")
	}
}

// TestNormalizeTranscripts replays transcripts captured from a real pi --mode rpc run against
// a llama.cpp router.
func TestNormalizeTranscripts(t *testing.T) {
	tests := []struct {
		file      string
		stop      string
		cancelled bool
	}{
		{file: "plain.jsonl", stop: "stop"},
		{file: "aborted.jsonl", stop: "aborted", cancelled: true},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			var events []harness.Event
			for _, line := range readLines(t, tt.file) {
				r, err := decode(line)
				if err != nil {
					t.Fatal(err)
				}
				if r.Type == "response" {
					continue
				}
				events = append(events, normalize(r, line)...)
			}

			if events[0].Kind != harness.EventStarted {
				t.Errorf("first kind = %s, want started", events[0].Kind)
			}
			if k := events[len(events)-1].Kind; k != harness.EventEnded {
				t.Errorf("last kind = %s, want ended", k)
			}

			var deltas strings.Builder
			var end *harness.Event
			cancelled := false
			for i, ev := range events {
				switch ev.Kind {
				case harness.EventTextDelta:
					deltas.WriteString(ev.Text)
				case harness.EventMessageEnd:
					end = &events[i]
				case harness.EventCancelled:
					cancelled = true
				}
			}
			if end == nil {
				t.Fatal("no assistant message_end")
			}
			if end.StopReason != tt.stop {
				t.Errorf("StopReason = %q, want %q", end.StopReason, tt.stop)
			}
			if deltas.String() != end.Text {
				t.Errorf("deltas %q do not add up to the message text %q", deltas.String(), end.Text)
			}
			if cancelled != tt.cancelled {
				t.Errorf("cancelled = %v, want %v", cancelled, tt.cancelled)
			}
		})
	}
}

func readLines(t *testing.T, name string) [][]byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		lines = append(lines, bytes.Clone(s.Bytes()))
	}
	return lines
}

func kinds(events []harness.Event) []harness.EventKind {
	out := make([]harness.EventKind, len(events))
	for i, ev := range events {
		out[i] = ev.Kind
	}
	return out
}

func TestCodecResponses(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr string
	}{
		{name: "success", line: `{"id":"7","type":"response","command":"get_state","success":true,"data":{"sessionId":"s"}}`},
		{
			name:    "failure",
			line:    `{"id":"7","type":"response","command":"prompt","success":false,"error":"Agent is already processing."}`,
			wantErr: "pi: prompt: Agent is already processing.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := codec{}.Decode([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			if f.Response == nil || f.Response.ID != "7" || len(f.Events) != 0 {
				t.Fatalf("frame = %+v, want only a response with id 7", f)
			}
			if got := fmt.Sprint(f.Response.Err); (f.Response.Err != nil || tt.wantErr != "") && got != tt.wantErr {
				t.Fatalf("Err = %v, want %q", f.Response.Err, tt.wantErr)
			}
		})
	}
}

func TestCodecEncodeStampsID(t *testing.T) {
	line, err := codec{}.Encode("42", command{Type: "abort"})
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != `{"id":"42","type":"abort"}` {
		t.Fatalf("Encode = %s", line)
	}
	if _, err := (codec{}).Encode("1", "abort"); err == nil {
		t.Fatal("Encode accepted a value that is not a command")
	}
}
