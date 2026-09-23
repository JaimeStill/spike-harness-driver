package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeEnv names the variable that turns the test binary into a fake Pi, and selects its
// behavior: "ok" answers prompts, and "crash" dies during a run.
const fakeEnv = "PI_FAKE"

// longPrompt makes the fake stream text deltas until it is aborted.
const longPrompt = "long"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakePi(mode))
	}
	os.Exit(m.Run())
}

// fakeDriver returns a Driver whose Pi is this test binary in fake mode.
func fakeDriver(mode string) Driver {
	// A race-enabled binary sleeps a second at exit unless told not to.
	return Driver{Command: os.Args[0], Env: []string{fakeEnv + "=" + mode, "GORACE=atexit_sleep_ms=0"}}
}

// fakePi speaks Pi's RPC protocol on stdin and stdout. A normal prompt replays the events of
// the captured plain.jsonl transcript. The long prompt streams deltas until an abort arrives,
// then ends the way the captured aborted.jsonl transcript does, including answering the abort
// only after agent_settled.
func fakePi(mode string) int {
	var mu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	emit := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.WriteString(line + "\n")
		_ = out.Flush()
	}
	respond := func(c command, extra string) {
		emit(fmt.Sprintf(`{"id":%q,"type":"response","command":%q,"success":true%s}`, c.ID, c.Type, extra))
	}

	var (
		runMu   sync.Mutex
		running bool
		abort   = make(chan chan struct{}, 1)
	)
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var c command
		if err := json.Unmarshal(in.Bytes(), &c); err != nil {
			return 2
		}
		switch c.Type {
		case "set_model", "clear_queue":
			respond(c, "")
		case "get_state":
			respond(c, `,"data":{"sessionId":"fake-session"}`)
		case "abort":
			done := make(chan struct{})
			abort <- done
			go func() {
				<-done
				respond(c, "")
			}()
		case "prompt":
			runMu.Lock()
			if running {
				runMu.Unlock()
				emit(fmt.Sprintf(`{"id":%q,"type":"response","command":"prompt","success":false,"error":"Agent is already processing."}`, c.ID))
				continue
			}
			running = true
			runMu.Unlock()
			respond(c, "")
			go func() {
				defer func() { runMu.Lock(); running = false; runMu.Unlock() }()
				switch {
				case mode == "crash":
					emit(`{"type":"agent_start"}`)
					fmt.Fprintln(os.Stderr, "boom: model backend unreachable")
					os.Exit(3)
				case c.Message == longPrompt:
					streamUntilAbort(emit, abort)
				default:
					replay(emit, "testdata/plain.jsonl")
				}
			}()
		}
	}
	return 0
}

func replay(emit func(string), file string) {
	data, err := os.Open(file)
	if err != nil {
		panic(err)
	}
	defer func() { _ = data.Close() }()
	s := bufio.NewScanner(data)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		var r record
		if json.Unmarshal(s.Bytes(), &r) == nil && r.Type != "response" {
			emit(s.Text())
		}
	}
}

func streamUntilAbort(emit func(string), abort chan chan struct{}) {
	emit(`{"type":"agent_start"}`)
	emit(`{"type":"turn_start"}`)
	emit(`{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"pending"}}`)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case done := <-abort:
			msg := `{"role":"assistant","content":[{"type":"text","text":"..."}],"stopReason":"aborted","errorMessage":"Request was aborted"}`
			emit(`{"type":"message_end","message":` + msg + `}`)
			emit(`{"type":"turn_end","message":` + msg + `,"toolResults":[]}`)
			emit(`{"type":"agent_end","messages":[],"willRetry":false}`)
			emit(`{"type":"agent_settled"}`)
			close(done)
			return
		case <-tick.C:
			emit(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"."}}`)
		}
	}
}
