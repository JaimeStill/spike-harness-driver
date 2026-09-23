package pi

import (
	"bufio"
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		kinds []harness.Kind
		text  string
		stop  string
		tool  string
		err   string
	}{
		{name: "agent start", line: `{"type":"agent_start"}`, kinds: []harness.Kind{harness.KindStarted}},
		{name: "agent settled", line: `{"type":"agent_settled"}`, kinds: []harness.Kind{harness.KindEnded}},
		{
			name:  "text delta",
			line:  `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hi"}}`,
			kinds: []harness.Kind{harness.KindTextDelta}, text: "Hi",
		},
		{
			name:  "thinking delta",
			line:  `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"hm"}}`,
			kinds: []harness.Kind{harness.KindThinkingDelta}, text: "hm",
		},
		{
			name:  "text start is harness noise",
			line:  `{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}`,
			kinds: []harness.Kind{harness.KindHarness},
		},
		{
			name:  "tool call",
			line:  `{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"ls"}}`,
			kinds: []harness.Kind{harness.KindToolCall}, tool: "bash",
		},
		{
			name:  "tool result",
			line:  `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[]},"isError":false}`,
			kinds: []harness.Kind{harness.KindToolResult}, tool: "bash",
		},
		{
			name:  "assistant message end",
			line:  `{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"Go."}],"stopReason":"stop","usage":{"input":3,"output":2}}}`,
			kinds: []harness.Kind{harness.KindMessageEnd}, text: "Go.", stop: "stop",
		},
		{
			name:  "aborted message end",
			line:  `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"aborted","errorMessage":"Request was aborted"}}`,
			kinds: []harness.Kind{harness.KindMessageEnd, harness.KindCancelled}, stop: "aborted",
		},
		{
			name:  "errored message end",
			line:  `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"boom"}}`,
			kinds: []harness.Kind{harness.KindMessageEnd, harness.KindError}, stop: "error", err: "boom",
		},
		{
			name:  "user message end is harness noise",
			line:  `{"type":"message_end","message":{"role":"user","content":"hi"}}`,
			kinds: []harness.Kind{harness.KindHarness},
		},
		{name: "unknown event", line: `{"type":"something_new"}`, kinds: []harness.Kind{harness.KindHarness}},
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
			if last.Err != tt.err {
				t.Errorf("Err = %q, want %q", last.Err, tt.err)
			}
			if string(first.Raw) != tt.line {
				t.Errorf("Raw = %s, want the input line", first.Raw)
			}
		})
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

			if events[0].Kind != harness.KindStarted {
				t.Errorf("first kind = %s, want started", events[0].Kind)
			}
			if k := events[len(events)-1].Kind; k != harness.KindEnded {
				t.Errorf("last kind = %s, want ended", k)
			}

			var deltas strings.Builder
			var end *harness.Event
			cancelled := false
			for i, ev := range events {
				switch ev.Kind {
				case harness.KindTextDelta:
					deltas.WriteString(ev.Text)
				case harness.KindMessageEnd:
					end = &events[i]
				case harness.KindCancelled:
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

func kinds(events []harness.Event) []harness.Kind {
	out := make([]harness.Kind, len(events))
	for i, ev := range events {
		out[i] = ev.Kind
	}
	return out
}
