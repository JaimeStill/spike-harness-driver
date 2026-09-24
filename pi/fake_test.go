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

// The prompts that play the bridge's part. A toolPrompt calls the tool named after its colon
// with {"q":"<rest>"} and ends the run with the tool's result as the text; structuredPrompt
// calls respond, when the exchange has a schema; dialogPrompt raises a confirm dialog the
// driver must dismiss before the run goes on.
const (
	toolPrompt       = "tool:"
	structuredPrompt = "structured"
	dialogPrompt     = "dialog"
)

// launchEnv names the file where the fake records how it was launched: its arguments, the
// tool spec the bridge would read, and the files of each skill it was given.
const launchEnv = "PI_FAKE_LAUNCH"

// launch is what the fake records of its launch.
type launch struct {
	Args      []string            `json:"args"`
	Extension bool                `json:"extension"` // the -e file exists
	Tools     json.RawMessage     `json:"tools"`
	Skills    map[string][]string `json:"skills"` // each --skill directory's files, by the directory
}

func recordLaunch(args []string) {
	file := os.Getenv(launchEnv)
	if file == "" {
		return
	}
	l := launch{Args: args, Skills: map[string][]string{}}
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-e":
			_, err := os.Stat(args[i+1])
			l.Extension = err == nil
		case "--skill":
			dir := args[i+1]
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(dir, p)
					l.Skills[dir] = append(l.Skills[dir], rel)
				}
				return nil
			})
		}
	}
	l.Tools, _ = os.ReadFile(os.Getenv(toolsEnv))
	b, _ := json.Marshal(l)
	_ = os.WriteFile(file, b, 0o600)
}

// dialogs routes the driver's answers to the fake's dialogs, by the dialog's id.
type fakeDialogs struct {
	mu      sync.Mutex
	next    int
	waiting map[string]chan dialogAnswer
}

// ask emits a dialog and waits for the driver's answer.
func (d *fakeDialogs) ask(emit func(string), method, title, placeholder string) dialogAnswer {
	d.mu.Lock()
	d.next++
	id := fmt.Sprintf("ui-%d", d.next)
	ch := make(chan dialogAnswer, 1)
	d.waiting[id] = ch
	d.mu.Unlock()
	emit(fmt.Sprintf(`{"type":"extension_ui_request","id":%q,"method":%q,"title":%q,"placeholder":%q}`, id, method, title, placeholder))
	return <-ch
}

func (d *fakeDialogs) answer(a dialogAnswer) {
	d.mu.Lock()
	ch := d.waiting[a.ID]
	delete(d.waiting, a.ID)
	d.mu.Unlock()
	if ch != nil {
		ch <- a
	}
}

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

// fakeJournal is the fake Pi's session: its entries in append order, shaped as the capture in
// testdata/entries.jsonl shows Pi's. Setting the model on a new session appends three
// entries (model_change, thinking_level_change, model_change), and on a resumed one appends
// one; a session's first run appends a system message before the user message.
//
// With --session-dir it persists the entries to <dir>/<session-id>.entries, one "id kind"
// line each, so a later fake process resumes the session, and deleting the file loses it, as
// losing Pi's session file would.
type fakeJournal struct {
	mu      sync.Mutex
	id      string
	path    string
	entries []string // IDs
	kinds   []string // each entry's kind, as the fake names it
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
		for line := range strings.Lines(string(data)) {
			if id, kind, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
				j.entries, j.kinds = append(j.entries, id), append(j.kinds, kind)
			}
		}
	}
	return j
}

// setModel appends the entries Pi appends when the model is set.
func (j *fakeJournal) setModel() {
	if j.empty() {
		j.append("model_change")
		j.append("thinking_level_change")
	}
	j.append("model_change")
}

// startRun appends the entries Pi appends as a run starts.
func (j *fakeJournal) startRun() {
	j.mu.Lock()
	system := !slices.Contains(j.kinds, "system")
	j.mu.Unlock()
	if system {
		j.append("system")
	}
	j.append("user")
}

func (j *fakeJournal) empty() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries) == 0
}

// append adds one entry of kind. IDs are unique within the session, across processes.
func (j *fakeJournal) append(kind string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.next++
	id := fmt.Sprintf("%04d%04d", len(j.entries), j.next)
	j.entries, j.kinds = append(j.entries, id), append(j.kinds, kind)
	if j.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(j.path), 0o755)
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		panic(err)
	}
	_, _ = f.WriteString(id + " " + kind + "\n")
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
	recordLaunch(os.Args[1:])
	journal := newFakeJournal(os.Args[1:])
	dialogs := &fakeDialogs{waiting: map[string]chan dialogAnswer{}}
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
		case "extension_ui_response":
			var a dialogAnswer
			if err := json.Unmarshal(in.Bytes(), &a); err != nil {
				return 2
			}
			dialogs.answer(a)
		case "set_model":
			journal.setModel()
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
			// Pi appends the user message (and a session's first system message) as the run
			// starts, and the assistant message as it ends. The run is over by the time Pi reports agent_settled, so it accepts a prompt
			// sent in response to it.
			journal.startRun()
			runEmit := func(line string) {
				if strings.Contains(line, `"type":"message_end"`) && strings.Contains(line, `"role":"assistant"`) {
					journal.append("assistant")
				}
				if strings.Contains(line, `"type":"agent_settled"`) {
					runMu.Lock()
					running = false
					runMu.Unlock()
				}
				emit(line)
			}
			go func() {
				// The bridge asks for the exchange's schema as the prompt arrives.
				var ex answer
				a := dialogs.ask(emit, "input", exchangeTitle, "{}")
				if a.Value == nil || json.Unmarshal([]byte(*a.Value), &ex) != nil {
					emit(`{"type":"extension_error","extensionPath":"bridge.ts","event":"before_agent_start","error":"no answer"}`)
				}
				switch {
				case strings.HasPrefix(c.Message, toolPrompt):
					callTool(runEmit, dialogs, strings.TrimPrefix(c.Message, toolPrompt))
				case c.Message == structuredPrompt && ex.Schema != nil:
					respondStructured(runEmit)
				case c.Message == dialogPrompt:
					if a := dialogs.ask(emit, "confirm", "Proceed?", ""); !a.Cancelled {
						emit(`{"type":"extension_error","extensionPath":"other.ts","event":"tool_call","error":"the confirm dialog was answered, not dismissed"}`)
					}
					replay(runEmit, "testdata/plain.jsonl")
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

// callTool runs one bridge tool call: name is the tool and q its argument. The run's text is
// the tool's result, or its error.
func callTool(emit func(string), dialogs *fakeDialogs, spec string) {
	name, q, _ := strings.Cut(spec, " ")
	args, _ := json.Marshal(map[string]string{"q": q})
	body, _ := json.Marshal(call{Tool: name, CallID: "call-1", Args: args})
	emit(`{"type":"agent_start"}`)
	emit(fmt.Sprintf(`{"type":"tool_execution_start","toolCallId":"call-1","toolName":%q,"args":%s}`, name, args))
	a := dialogs.ask(emit, "input", callTitle, string(body))
	var ans answer
	text, isError := "", false
	switch {
	case a.Value == nil || json.Unmarshal([]byte(*a.Value), &ans) != nil:
		text, isError = "no answer", true
	case ans.Error != nil:
		text, isError = *ans.Error, true
	case ans.Result != nil:
		text = *ans.Result
	}
	result, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}})
	emit(fmt.Sprintf(`{"type":"tool_execution_end","toolCallId":"call-1","toolName":%q,"result":%s,"isError":%t}`, name, result, isError))
	msg, _ := json.Marshal(map[string]any{
		"role": "assistant", "content": []map[string]string{{"type": "text", "text": text}},
		"stopReason": "stop", "usage": map[string]int{"input": 5, "output": 2, "cacheRead": 700},
	})
	emit(`{"type":"message_end","message":` + string(msg) + `}`)
	emit(`{"type":"agent_end","messages":[],"willRetry":false}`)
	emit(`{"type":"agent_settled"}`)
}

// respondStructured ends a run on the respond tool, as the bridge's terminating tool does.
func respondStructured(emit func(string)) {
	emit(`{"type":"agent_start"}`)
	emit(`{"type":"tool_execution_start","toolCallId":"call-r","toolName":"respond","args":{"answer":42}}`)
	emit(`{"type":"tool_execution_end","toolCallId":"call-r","toolName":"respond","result":{"content":[{"type":"text","text":"Recorded."}],"details":{"answer":42},"terminate":true},"isError":false}`)
	emit(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-r","name":"respond","arguments":{"answer":42}}],"stopReason":"toolUse"}}`)
	emit(`{"type":"agent_end","messages":[],"willRetry":false}`)
	emit(`{"type":"agent_settled"}`)
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
