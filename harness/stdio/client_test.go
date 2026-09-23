package stdio_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	ID    string  `json:"id"`
	OK    bool    `json:"ok"`
	Error string  `json:"error"`
	Data  string  `json:"data"`
	Event *string `json:"event"`
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
	if l.Event != nil {
		return stdio.Frame{Events: []harness.Event{{Kind: harness.EventTextDelta, Text: *l.Event}}}, nil
	}
	r := &stdio.Response{ID: l.ID, Data: json.RawMessage(fmt.Sprintf("%q", l.Data))}
	if !l.OK {
		r.Err = errors.New(l.Error)
	}
	return stdio.Frame{Response: r}, nil
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
			orphan()
			os.Exit(0)
		}
	}
	switch mode {
	case "stubborn":
		select {}
	case "orphaner":
		orphan()
	}
	return 0
}

// orphan starts a child that inherits the peer's stdout and outlives the peer, as a tool
// process a harness started might.
func orphan() {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), peerEnv+"=sleep")
	cmd.Stdout = os.Stdout
	_ = cmd.Start()
}

func start(t *testing.T, mode string, waitDelay time.Duration) *stdio.Client {
	t.Helper()
	p, err := stdio.Start(stdio.Spec{
		Name: os.Args[0],
		// A race-enabled binary sleeps a second at exit unless told not to.
		Env:       []string{peerEnv + "=" + mode, "GORACE=atexit_sleep_ms=0"},
		WaitDelay: waitDelay,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stdio.NewClient(p, testCodec{})
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
	ev, _ := nextEvent(t, c)
	if ev.Kind != harness.EventError || !strings.Contains(ev.Err, "fatal: peer gone") {
		t.Fatalf("event = %+v, want EventError with stderr", ev)
	}
	if _, ok := nextEvent(t, c); ok {
		t.Fatal("Events did not close after the exit")
	}
	if _, err := c.Call(t.Context(), testCmd{Op: "echo", Data: "1"}); err == nil {
		t.Fatal("Call after exit succeeded")
	}
	if err := c.Close(); err == nil {
		t.Fatal("Close after a failed exit returned nil")
	}
}

func TestClose(t *testing.T) {
	tests := []struct {
		name string
		mode string
		// wantErr is nil when Close may return either way.
		wantErr *bool
	}{
		{name: "exits on end of input", mode: "echo", wantErr: new(false)},
		{name: "killed after the wait delay", mode: "stubborn", wantErr: new(true)},
		{name: "a child holds stdout", mode: "orphaner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := start(t, tt.mode, 200*time.Millisecond)
			begin := time.Now()
			err := c.Close()
			if d := time.Since(begin); d > 3*time.Second {
				t.Fatalf("Close took %s", d)
			}
			if tt.wantErr != nil && (err != nil) != *tt.wantErr {
				t.Fatalf("Close = %v, want error %v", err, *tt.wantErr)
			}
			ev, ok := nextEvent(t, c)
			if ok && tt.wantErr != nil && !*tt.wantErr {
				t.Fatalf("a clean Close reported %+v", ev)
			}
		})
	}
}

// TestExitWithChildHoldingStdout covers a harness that exits on its own while a child still
// holds its stdout: the wait delay closes the pipe, so the exit is still seen.
func TestExitWithChildHoldingStdout(t *testing.T) {
	c := start(t, "echo", 200*time.Millisecond)
	begin := time.Now()
	if _, err := c.Call(t.Context(), testCmd{Op: "orphan"}); err == nil {
		t.Fatal("Call to a peer that exited succeeded")
	}
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("the exit took %s to be seen", d)
	}
	ev, _ := nextEvent(t, c)
	if ev.Kind != harness.EventError {
		t.Fatalf("event = %+v, want EventError", ev)
	}
	if _, ok := nextEvent(t, c); ok {
		t.Fatal("Events did not close after the exit")
	}
}
