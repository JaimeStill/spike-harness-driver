package stdio_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// peerEnv turns the test binary into a line-protocol peer, and selects its behavior: "echo"
// answers commands, "stubborn" also ignores the end of its input, "orphaner" leaves a child
// holding its stdout when its input ends, and "sleep" is that child.
const peerEnv = "STDIO_PEER"

func TestMain(m *testing.M) {
	if os.Getenv(peerEnv) == "sleep" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if mode := os.Getenv(peerEnv); mode != "" {
		os.Exit(peer(mode))
	}
	os.Exit(m.Run())
}

// testCmd is the peer's command; testCodec encodes it and decodes the peer's lines.
type testCmd struct {
	ID   string `json:"id"`
	Op   string `json:"op"`
	Data string `json:"data,omitempty"`
}

type testLine struct {
	ID string `json:"id"`
	// Request, when set, makes the line a request of the driver, with Data as its body.
	Request string  `json:"request,omitempty"`
	OK      bool    `json:"ok"`
	Error   string  `json:"error"`
	Data    string  `json:"data"`
	Event   *string `json:"event"`
}

type testCodec struct{}

func (testCodec) Encode(id string, cmd any) ([]byte, error) {
	c := cmd.(testCmd)
	c.ID = id
	return json.Marshal(c)
}

func (testCodec) Decode(line []byte) (stdio.Frame, error) {
	var l testLine
	if err := json.Unmarshal(line, &l); err != nil {
		return stdio.Frame{}, err
	}
	if l.Request != "" {
		return stdio.Frame{Request: &stdio.Request{ID: l.Request, Body: l.Data}}, nil
	}
	if l.Event != nil {
		return stdio.Frame{Events: []harness.Event{{Kind: harness.EventTextDelta, Text: *l.Event}}}, nil
	}
	r := &stdio.Response{ID: l.ID, Data: json.RawMessage(fmt.Sprintf("%q", l.Data))}
	if !l.OK {
		r.Err = errors.New(l.Error)
	}
	return stdio.Frame{Response: r}, nil
}

func (testCodec) Reply(req stdio.Request, answer any) ([]byte, error) {
	return json.Marshal(testCmd{ID: req.ID, Op: "reply", Data: answer.(string)})
}

// peer answers each command on its own goroutine, after a delay that reverses their order, so
// responses arrive out of order.
func peer(mode string) int {
	var mu sync.Mutex
	emit := func(v any) {
		b, _ := json.Marshal(v)
		// encoding/json escapes U+2028; a harness written in another language may not.
		b = bytes.ReplaceAll(b, []byte(`\u2028`), []byte("\u2028"))
		mu.Lock()
		defer mu.Unlock()
		_, _ = os.Stdout.Write(append(b, '\n'))
	}
	event := func(s string) { emit(testLine{Event: &s}) }

	// asked maps each request the peer made of the driver to the command that made it, which
	// the driver's answer then answers.
	asked := map[string]string{}
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var c testCmd
		if err := json.Unmarshal(in.Bytes(), &c); err != nil {
			return 2
		}
		switch c.Op {
		case "echo":
			go func() {
				var n int
				_, _ = fmt.Sscan(c.Data, &n)
				time.Sleep(time.Duration(50-n) * time.Millisecond)
				emit(testLine{ID: c.ID, OK: true, Data: c.Data})
			}()
		case "fail":
			emit(testLine{ID: c.ID, Error: "nope"})
		case "ask":
			asked["q"+c.ID] = c.ID
			emit(testLine{Request: "q" + c.ID, Data: c.Data})
		case "reply":
			emit(testLine{ID: asked[c.ID], OK: true, Data: c.Data})
		case "event":
			event(c.Data)
			emit(testLine{ID: c.ID, OK: true})
		case "big":
			event(strings.Repeat("x", 100_000))
			emit(testLine{ID: c.ID, OK: true})
		case "die":
			fmt.Fprintln(os.Stderr, "fatal: peer gone")
			os.Exit(4)
		case "orphan":
			orphan(false)
			os.Exit(0)
		case "escape":
			orphan(true)
			os.Exit(0)
		}
	}
	switch mode {
	case "stubborn":
		select {}
	case "orphaner":
		orphan(false)
	}
	return 0
}

// orphanPIDEnv names the file where orphan records its child's PID, for the test to check.
const orphanPIDEnv = "STDIO_ORPHAN_PID"

// orphan starts a child that inherits the peer's stdout and outlives the peer, as a tool
// process a harness started might. A detached child leaves the peer's process group, as a
// daemonizing process would.
func orphan(detached bool) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), peerEnv+"=sleep")
	cmd.Stdout = os.Stdout
	if detached {
		detach(cmd)
	}
	if cmd.Start() == nil {
		_ = os.WriteFile(os.Getenv(orphanPIDEnv), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}
}

func start(t *testing.T, mode string, waitDelay time.Duration, env ...string) *stdio.Client {
	t.Helper()
	return startAnswering(t, mode, waitDelay, nil, env...)
}

// startAnswering starts the peer with handle answering its requests.
func startAnswering(t *testing.T, mode string, waitDelay time.Duration, handle stdio.Handler, env ...string) *stdio.Client {
	t.Helper()
	p, err := stdio.Start(stdio.Spec{
		Name: os.Args[0],
		// A race-enabled binary sleeps a second at exit unless told not to.
		Env:       append([]string{peerEnv + "=" + mode, "GORACE=atexit_sleep_ms=0"}, env...),
		WaitDelay: waitDelay,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stdio.NewClient(p, testCodec{}, handle)
}

func nextEvent(t *testing.T, c *stdio.Client) (harness.Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		return ev, ok
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return harness.Event{}, false
	}
}

func TestCallsCorrelateOutOfOrder(t *testing.T) {
	c := start(t, "echo", 0)
	defer func() { _ = c.Close() }()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			want := fmt.Sprint(i)
			r, err := c.Call(t.Context(), testCmd{Op: "echo", Data: want})
			if err != nil {
				t.Error(err)
				return
			}
			var got string
			_ = json.Unmarshal(r.Data, &got)
			if got != want {
				t.Errorf("call %s got response %s", want, got)
			}
		})
	}
	wg.Wait()
}

func TestCallReportsHarnessFailure(t *testing.T) {
	c := start(t, "echo", 0)
	defer func() { _ = c.Close() }()
	if _, err := c.Call(t.Context(), testCmd{Op: "fail"}); err == nil || err.Error() != "nope" {
		t.Fatalf("Call = %v, want the harness's error", err)
	}
}

func TestLines(t *testing.T) {
	tests := []struct {
		name string
		cmd  testCmd
		want string
	}{
		{name: "longer than a scanner token", cmd: testCmd{Op: "big"}, want: strings.Repeat("x", 100_000)},
		// The peer writes U+2028 raw; a splitter that treats it as a line break, as
		// Node's readline does, would cut this line in two.
		{name: "holds U+2028", cmd: testCmd{Op: "event", Data: "a b"}, want: "a b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := start(t, "echo", 0)
			defer func() { _ = c.Close() }()
			if _, err := c.Call(t.Context(), tt.cmd); err != nil {
				t.Fatal(err)
			}
			ev, _ := nextEvent(t, c)
			if ev.Text != tt.want {
				t.Fatalf("event text has %d bytes, want %d", len(ev.Text), len(tt.want))
			}
		})
	}
}

func TestPeerExit(t *testing.T) {
	c := start(t, "echo", 0)
	_, err := c.Call(t.Context(), testCmd{Op: "die"})
	if err == nil || !strings.Contains(err.Error(), "fatal: peer gone") {
		t.Fatalf("pending Call = %v, want the exit error with stderr", err)
	}
	// The exit is Err's to report, not an event's.
	if ev, ok := nextEvent(t, c); ok {
		t.Fatalf("Events delivered %+v instead of closing after the exit", ev)
	}
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "fatal: peer gone") {
		t.Fatalf("Err = %v, want the exit error with stderr", err)
	}
	if _, err := c.Call(t.Context(), testCmd{Op: "echo", Data: "1"}); err == nil {
		t.Fatal("Call after exit succeeded")
	}
	if err := c.Close(); err == nil {
		t.Fatal("Close after a failed exit returned nil")
	}
}

func TestRequestsAreAnswered(t *testing.T) {
	// Each answer waits for the one before it to be asked, so the requests are answered
	// only if they are answered concurrently.
	const n = 10
	var asked sync.WaitGroup
	asked.Add(n)
	c := startAnswering(t, "echo", 0, func(ctx context.Context, req stdio.Request) any {
		asked.Done()
		asked.Wait()
		return strings.ToUpper(req.Body.(string))
	})
	defer func() { _ = c.Close() }()

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			q := fmt.Sprintf("question %d", i)
			r, err := c.Call(t.Context(), testCmd{Op: "ask", Data: q})
			if err != nil {
				t.Error(err)
				return
			}
			var got string
			_ = json.Unmarshal(r.Data, &got)
			if got != strings.ToUpper(q) {
				t.Errorf("asked %q, got %q", q, got)
			}
		})
	}
	wg.Wait()
}

func TestARequestWithNoHandlerIsAnError(t *testing.T) {
	c := start(t, "echo", 0)
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, testCmd{Op: "ask", Data: "anyone?"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call = %v, want no answer", err)
	}
	ev, _ := nextEvent(t, c)
	if ev.Kind != harness.EventError || ev.Err == nil || !strings.Contains(ev.Err.Error(), "no handler") {
		t.Fatalf("event = %+v, want EventError for the unanswered request", ev)
	}
}

// An answer in progress when the harness goes away has its context ended, and the Client
// shuts down once it returns.
func TestCloseEndsAnAnswerInProgress(t *testing.T) {
	answering := make(chan struct{})
	ended := make(chan struct{})
	c := startAnswering(t, "echo", 0, func(ctx context.Context, _ stdio.Request) any {
		close(answering)
		<-ctx.Done()
		close(ended)
		return ""
	})
	go func() { _, _ = c.Call(context.Background(), testCmd{Op: "ask", Data: "slow"}) }()
	<-answering
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the answer's context did not end")
	}
	if _, ok := nextEvent(t, c); ok {
		t.Fatal("Events did not close")
	}
}

func TestClose(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "exits on end of input", mode: "echo"},
		{name: "killed after the wait delay", mode: "stubborn", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := start(t, tt.mode, 200*time.Millisecond)
			begin := time.Now()
			err := c.Close()
			if d := time.Since(begin); d > 3*time.Second {
				t.Fatalf("Close took %s", d)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("Close = %v, want error %v", err, tt.wantErr)
			}
			if ev, ok := nextEvent(t, c); ok {
				t.Fatalf("Close reported %+v", ev)
			}
			// Close ended the harness, however it went, so Err has no exit to report.
			if err := c.Err(); err != nil {
				t.Fatalf("Err after Close = %v", err)
			}
		})
	}
}

// TestCloseDiscardsUnreadEvents covers a Client whose events no one reads, as after a failed
// handshake: Close ends its event queue, so nothing is left blocked holding them.
func TestCloseDiscardsUnreadEvents(t *testing.T) {
	c := start(t, "echo", 0)
	if _, err := c.Call(t.Context(), testCmd{Op: "event", Data: "unread"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if ev, ok := nextEvent(t, c); ok {
		t.Fatalf("Close delivered %+v instead of discarding it", ev)
	}
}
