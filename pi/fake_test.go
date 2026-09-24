package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// fakeJournal is the fake Pi's session: entry IDs in append order. With --session-dir it
// persists them to <dir>/<session-id>.entries, one per line, so a later fake process resumes
// the session, and deleting the file loses it, as losing Pi's session file would.
type fakeJournal struct {
	mu      sync.Mutex
	id      string
	path    string
	entries []string
	next    int
}

func newFakeJournal(args []string) *fakeJournal {
	j := &fakeJournal{id: "fake-session"}
	dir := ""
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--session-id":
			j.id = args[i+1]
		case "--session-dir":
			dir = args[i+1]
		}
	}
	if dir == "" {
		return j
	}
	j.path = filepath.Join(dir, j.id+".entries")
	if data, err := os.ReadFile(j.path); err == nil {
		j.entries = strings.Fields(string(data))
	}
	return j
}

// append adds one entry and returns its ID. IDs are unique within the session, across
// processes.
func (j *fakeJournal) append() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.next++
	id := fmt.Sprintf("%04d%04d", len(j.entries), j.next)
	j.entries = append(j.entries, id)
	if j.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(j.path), 0o755)
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		panic(err)
	}
	_, _ = f.WriteString(id + "\n")
	_ = f.Close()
}

// since renders a get_entries response's data, or false when since names no entry.
func (j *fakeJournal) since(since string) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	from := 0
	if since != "" {
		i := slices.Index(j.entries, since)
		if i < 0 {
			return "", false
		}
		from = i + 1
	}
	var b strings.Builder
	for i, id := range j.entries[from:] {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"message","id":%q}`, id)
	}
	return `{"entries":[` + b.String() + `]}`, true
}

// fakePi speaks Pi's RPC protocol on stdin and stdout. A normal prompt replays the events of
// the captured plain.jsonl transcript. The long prompt streams deltas until an abort arrives,
// then ends the way the captured aborted.jsonl transcript does, including answering the abort
// only after agent_settled.
func fakePi(mode string) int {
	journal := newFakeJournal(os.Args[1:])
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
		case "set_model":
			journal.append()
			respond(c, "")
		case "clear_queue":
			respond(c, "")
		case "get_state":
			respond(c, fmt.Sprintf(`,"data":{"sessionId":%q}`, journal.id))
		case "get_entries":
			data, ok := journal.since(c.Since)
			if !ok {
				emit(fmt.Sprintf(`{"id":%q,"type":"response","command":"get_entries","success":false,"error":"Entry not found: %s"}`, c.ID, c.Since))
				continue
			}
			respond(c, `,"data":`+data)
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
			// Pi appends the user message as the run starts, and the assistant message as it
			// ends. The run is over by the time Pi reports agent_settled, so it accepts a prompt
			// sent in response to it.
			journal.append()
			runEmit := func(line string) {
				if strings.Contains(line, `"type":"message_end"`) && strings.Contains(line, `"role":"assistant"`) {
					journal.append()
				}
				if strings.Contains(line, `"type":"agent_settled"`) {
					runMu.Lock()
					running = false
					runMu.Unlock()
				}
				emit(line)
			}
			go func() {
				switch {
				case mode == "crash":
					runEmit(`{"type":"agent_start"}`)
					fmt.Fprintln(os.Stderr, "boom: model backend unreachable")
					os.Exit(3)
				case c.Message == longPrompt:
					streamUntilAbort(runEmit, abort)
				default:
					replay(runEmit, "testdata/plain.jsonl")
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
