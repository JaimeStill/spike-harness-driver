package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// openWith opens a fake Pi session with opts, recording its launch in the returned file.
func openWith(t *testing.T, opts harness.Options) (*harness.Session, string) {
	t.Helper()
	return openCached(t, opts, "")
}

// openCached opens a fake Pi session with opts and the driver cache in cache, recording its
// launch in the returned file.
func openCached(t *testing.T, opts harness.Options, cache string) (*harness.Session, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "launch.json")
	d := fakeDriver("ok")
	d.CacheDir = cache
	d.Env = append(d.Env, launchEnv+"="+file)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := d.Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s, file
}

// run runs req on s to its end.
func run(t *testing.T, s *harness.Session, req harness.Request) ([]harness.Event, harness.Result, error) {
	t.Helper()
	x, err := s.Send(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, x, 0, nil)
	checkScoped(t, s, x, events)
	res, err := x.Wait()
	return events, res, err
}

var lookup = harness.Tool{
	Name:        "lookup",
	Description: "Looks up a code.",
	Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	Handler: func(_ context.Context, args json.RawMessage) (string, error) {
		var a struct{ Q string }
		_ = json.Unmarshal(args, &a)
		if a.Q == "missing" {
			return "", errors.New("no code for missing")
		}
		return "code for " + a.Q, nil
	},
}

func TestAToolRunsInGo(t *testing.T) {
	s, _ := openWith(t, harness.Options{Tools: []harness.Tool{lookup}})
	defer closeSession(t, s)

	events, res, err := run(t, s, harness.Request{Text: toolPrompt + "lookup alice"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "code for alice" {
		t.Fatalf("result text = %q, want the Go handler's result", res.Text)
	}
	if !has(events, harness.EventToolCall) || !has(events, harness.EventToolResult) {
		t.Fatalf("no tool events in %v", kinds(events))
	}
	if res.Usage.CacheRead != 700 {
		t.Fatalf("usage = %+v, want the cache read", res.Usage)
	}

	// A handler's error reaches the model as a failed call; the exchange carries on.
	events, res, err = run(t, s, harness.Request{Text: toolPrompt + "lookup missing"})
	if err != nil {
		t.Fatal(err)
	}
	var failed *harness.ToolEvent
	for _, ev := range events {
		if ev.Kind == harness.EventToolResult {
			failed = ev.Tool
		}
	}
	if failed == nil || !failed.IsError || res.Text != "no code for missing" {
		t.Fatalf("tool result = %+v, text %q; want the handler's error", failed, res.Text)
	}
}

func TestAStructuredResponse(t *testing.T) {
	s, _ := openWith(t, harness.Options{})
	defer closeSession(t, s)

	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"integer"}}}`)
	events, res, err := run(t, s, harness.Request{Text: structuredPrompt, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Structured) != `{"answer":42}` || !has(events, harness.EventStructured) {
		t.Fatalf("result = %+v", res)
	}

	// Without a schema the bridge gives the run no respond tool, and the fake answers in text.
	_, res, err = run(t, s, harness.Request{Text: structuredPrompt})
	if err != nil || res.Structured != nil {
		t.Fatalf("no schema: result = %+v, %v", res, err)
	}
}

func TestAForeignDialogIsDismissed(t *testing.T) {
	s, _ := openWith(t, harness.Options{})
	defer closeSession(t, s)
	events, res, err := run(t, s, harness.Request{Text: dialogPrompt})
	if err != nil || res.StopReason != "stop" || has(events, harness.EventError) {
		t.Fatalf("result = %+v, %v; events %v", res, err, kinds(events))
	}
}

func TestTheLaunchLoadsTheBridgeToolsAndSkills(t *testing.T) {
	skill := harness.Skill{Name: "greet", FS: fstest.MapFS{
		"SKILL.md":        {Data: []byte("---\nname: greet\ndescription: Greets.\n---\nSay hi.\n")},
		"refs/phrases.md": {Data: []byte("hi\n")},
	}}
	s, file := openWith(t, harness.Options{
		Tools: []harness.Tool{lookup}, Skills: []harness.Skill{skill}, HarnessTools: []string{"read"},
	})
	var l launch
	data, err := os.ReadFile(file)
	if err == nil {
		err = json.Unmarshal(data, &l)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !l.Extension {
		t.Error("the bridge extension wasn't on disk at launch")
	}
	for _, want := range [][]string{rpcArgs, {"-t", "read,lookup,respond"}} {
		if !containsRun(l.Args, want) {
			t.Errorf("args %q lack %q", l.Args, want)
		}
	}
	var specs []toolSpec
	if err := json.Unmarshal(l.Tools, &specs); err != nil || len(specs) != 1 || specs[0].Name != "lookup" {
		t.Errorf("tool spec = %s", l.Tools)
	}
	files := l.Skills[arg(l.Args, "--skill")]
	slices.Sort(files)
	if !slices.Equal(files, []string{"SKILL.md", filepath.Join("refs", "phrases.md")}) {
		t.Errorf("skill files = %v", files)
	}

	ext := arg(l.Args, "-e")
	closeSession(t, s)
	if _, err := os.Stat(filepath.Dir(ext)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the bridge's files outlived Close: %v", err)
	}
}

func TestTheHarnessDefaultToolsNeedNoAllowlist(t *testing.T) {
	s, file := openWith(t, harness.Options{Tools: []harness.Tool{lookup}})
	defer closeSession(t, s)
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), `"-t"`) {
		t.Fatalf("launch %s has an allowlist, want Pi's default tools", data)
	}
}

func TestToolsTheBridgeRefuses(t *testing.T) {
	noHandler := lookup
	noHandler.Handler = nil
	respond := lookup
	respond.Name = respondTool
	for name, tools := range map[string][]harness.Tool{
		"no handler":    {noHandler},
		"respond":       {respond},
		"duplicate":     {lookup, lookup},
		"bad skill dir": nil,
	} {
		opts := harness.Options{Tools: tools}
		if name == "bad skill dir" {
			opts.Skills = []harness.Skill{{Name: "../escape", FS: fstest.MapFS{}}}
		}
		if _, err := fakeDriver("ok").Open(t.Context(), opts); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

// A tool call in progress ends when its exchange is cancelled.
func TestCancelEndsAToolCall(t *testing.T) {
	started := make(chan struct{})
	b, err := newBridge(harness.Options{Tools: []harness.Tool{{
		Name: "wait",
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.remove() }()
	c := newConnection(b)

	got := make(chan any, 1)
	go func() {
		got <- c.answer(t.Context(), stdioRequest(callTitle, `{"tool":"wait","callId":"c","args":{}}`))
	}()
	<-started
	c.mu.Lock()
	c.endRun()
	c.mu.Unlock()
	select {
	case a := <-got:
		if s, _ := a.(string); !strings.Contains(s, `"error"`) {
			t.Fatalf("answer = %v, want the call's error", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tool call didn't end")
	}
}

// readLaunch reads what the fake recorded of its launch.
func readLaunch(t *testing.T, file string) launch {
	t.Helper()
	var l launch
	data, err := os.ReadFile(file)
	if err == nil {
		err = json.Unmarshal(data, &l)
	}
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// arg returns the value that follows flag in args, or "" without one.
func arg(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func greetSkill(text string) harness.Skill {
	return harness.Skill{Name: "greet", FS: fstest.MapFS{
		"SKILL.md": {Data: []byte("---\nname: greet\ndescription: Greets.\n---\n" + text + "\n")},
	}}
}

// With a cache, the extension and a skill that isn't on disk get paths their content decides:
// the same across sessions, kept after Close, and new when the content changes.
func TestTheCacheGivesStablePaths(t *testing.T) {
	cache := t.TempDir()
	launchOf := func(skill harness.Skill) launch {
		s, file := openCached(t, harness.Options{Skills: []harness.Skill{skill}}, cache)
		closeSession(t, s)
		return readLaunch(t, file)
	}
	first, second := launchOf(greetSkill("Say hi.")), launchOf(greetSkill("Say hi."))
	for _, flag := range []string{"-e", "--skill"} {
		a, b := arg(first.Args, flag), arg(second.Args, flag)
		if a != b || !strings.HasPrefix(a, cache) {
			t.Errorf("%s: %s then %s, want one path under the cache", flag, a, b)
		}
		if _, err := os.Stat(a); err != nil {
			t.Errorf("%s: %s didn't outlive Close: %v", flag, a, err)
		}
	}
	if !strings.HasPrefix(filepath.Base(arg(first.Args, "--skill")), "greet-") {
		t.Errorf("skill directory %s isn't named for the skill", arg(first.Args, "--skill"))
	}
	changed := launchOf(greetSkill("Say hello."))
	if arg(changed.Args, "--skill") == arg(first.Args, "--skill") {
		t.Error("changed skill content kept the same path")
	}
}

// A skill already on disk is loaded where it lives, not copied.
func TestASkillOnDiskIsLoadedInPlace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "greet")
	if err := os.CopyFS(dir, greetSkill("Say hi.").FS); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	skill := greetSkill("Say hi.")
	skill.FS, skill.Dir = os.DirFS(dir), dir
	s, file := openCached(t, harness.Options{Skills: []harness.Skill{skill}}, cache)
	closeSession(t, s)
	if got := arg(readLaunch(t, file).Args, "--skill"); got != dir {
		t.Fatalf("--skill %s, want the skill's own directory %s", got, dir)
	}
	if _, err := os.Stat(filepath.Join(cache, "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the skill was copied into the cache: %v", err)
	}
}

// containsRun reports whether args hold want as a contiguous run.
func containsRun(args, want []string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		if slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func stdioRequest(title, placeholder string) stdio.Request {
	return stdio.Request{ID: "u", Body: dialog{Method: "input", Title: title, Placeholder: placeholder}}
}
