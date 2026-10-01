package claude

import (
	"bufio"
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
)

// fakeDriver returns a Driver whose Claude Code is this test binary in fake mode, recording its
// launch and prompts in dir, with dir as its configuration directory.
func fakeDriver(dir string) Driver {
	return Driver{
		Command:   os.Args[0],
		ConfigDir: dir,
		// A race-enabled binary sleeps a second at exit unless told not to.
		Env: []string{fakeEnv + "=1", "GORACE=atexit_sleep_ms=0",
			launchEnv + "=" + filepath.Join(dir, "launch"), promptsEnv + "=" + filepath.Join(dir, "prompts")},
	}
}

func open(t *testing.T, d Driver, opts harness.Options) *harness.Session {
	t.Helper()
	s, err := d.Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// exchange sends req and returns the exchange's events and result.
func exchange(t *testing.T, s *harness.Session, req harness.Request) ([]harness.Event, harness.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	x, err := s.Send(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var events []harness.Event
	for ev := range x.Events() {
		events = append(events, ev)
	}
	res, err := x.Wait()
	return events, res, err
}

// lines reads a file the fake recorded, one JSON value per line.
func lines[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestAnExchange(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{})
	events, res, err := exchange(t, s, harness.Request{Text: "hi"})
	if err != nil || res.Text != "hello" || res.StopReason != "end_turn" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if res.Usage != (harness.Usage{Input: 3, Output: 2, CacheRead: 5}) {
		t.Errorf("usage = %+v", res.Usage)
	}
	var text string
	for _, ev := range events {
		if ev.Kind == harness.EventTextDelta {
			text += ev.Text
		}
	}
	if text != "hello" || events[0].Kind != harness.EventStarted {
		t.Errorf("deltas = %q, first event %s", text, events[0].Kind)
	}
}

func TestToolsRunThroughTheDriversServer(t *testing.T) {
	var got json.RawMessage
	echo := harness.Tool{Name: "echo", Description: "echo",
		Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Handler: func(_ context.Context, args json.RawMessage) (string, error) {
			got = args
			return "ECHO", nil
		}}
	s := open(t, fakeDriver(t.TempDir()), harness.Options{Tools: []harness.Tool{echo}})

	events, res, err := exchange(t, s, harness.Request{Text: `tool:echo:{"text":"seven"}`})
	if err != nil || res.Text != "ECHO" || string(got) != `{"text":"seven"}` {
		t.Fatalf("result = %+v, %v; the handler got %s", res, err, got)
	}
	var tools []string
	for _, ev := range events {
		if ev.Tool != nil {
			tools = append(tools, string(ev.Kind)+" "+ev.Tool.Name)
		}
	}
	if !slices.Equal(tools, []string{"tool_call echo", "tool_result echo"}) {
		t.Errorf("tool events = %v", tools)
	}

	// Arguments that don't match the tool's schema fail without reaching the handler.
	got = nil
	_, res, _ = exchange(t, s, harness.Request{Text: `tool:echo:{"text":7}`})
	if got != nil || !strings.Contains(res.Text, "schema") {
		t.Errorf("a mistyped call reached the handler (%s) or wasn't refused: %q", got, res.Text)
	}
}

func TestAStructuredResponsePerSchema(t *testing.T) {
	dir := t.TempDir()
	s := open(t, fakeDriver(dir), harness.Options{})
	city := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	count := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`)

	_, res, err := exchange(t, s, harness.Request{Text: `structured:{"city":"Paris"}`, Schema: city})
	if err != nil || string(res.Structured) != `{"city":"Paris"}` {
		t.Fatalf("first = %s, %v", res.Structured, err)
	}
	// The second schema renames the respond tool; the fake answers with whatever respond it
	// listed last, so this passes only if the prompt waited for the new listing.
	events, res, err := exchange(t, s, harness.Request{Text: `structured:{"count":3}`, Schema: count})
	if err != nil || string(res.Structured) != `{"count":3}` {
		t.Fatalf("second = %s, %v", res.Structured, err)
	}
	for _, ev := range events {
		if ev.Kind == harness.EventToolCall || ev.Kind == harness.EventToolResult {
			t.Errorf("respond surfaced as a tool event: %+v", ev.Tool)
		}
	}
	// A response that fails the schema is rejected, and the exchange owes one still.
	events, _, err = exchange(t, s, harness.Request{Text: `structured:{"count":"three"}`, Schema: count})
	if !errors.Is(err, harness.ErrNoStructuredResponse) {
		t.Fatalf("error = %v, want ErrNoStructuredResponse", err)
	}
	if !slices.ContainsFunc(events, func(ev harness.Event) bool { return ev.Kind == harness.EventStructuredRejected }) {
		t.Errorf("no rejection among %d events", len(events))
	}

	prompts := lines[[]contentBlock](t, filepath.Join(dir, "prompts"))
	if text := prompts[1][0].Text; !strings.Contains(text, toolPrefix+respondNameOf(count)) {
		t.Errorf("prompt %q doesn't name the respond tool", text)
	}
}

// respondNameOf is mcpbridge's name for a schema's respond tool, read from a server.
func respondNameOf(schema json.RawMessage) string {
	srv, _ := newBridgeForTest()
	_ = srv.SetSchema(schema)
	return srv.RespondName()
}

func TestCancelInterruptsTheTurn(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{})
	x, err := s.Send(t.Context(), harness.Request{Text: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range x.Events() {
		if ev.Kind == harness.EventTextDelta {
			x.Cancel()
		}
	}
	res, err := x.Wait()
	if err != nil || res.StopReason != "aborted" {
		t.Fatalf("result = %+v, %v; want aborted", res, err)
	}
	if _, res, err = exchange(t, s, harness.Request{Text: "hi"}); err != nil || res.Text != "hello" {
		t.Fatalf("the next exchange = %+v, %v", res, err)
	}
}

// Claude Code exits with status 1 when its last turn failed, which an interrupted turn does;
// the exchange has reported it, so closing the session is no error.
func TestCloseAfterAnInterruptedTurn(t *testing.T) {
	s, err := fakeDriver(t.TempDir()).Open(t.Context(), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.Send(t.Context(), harness.Request{Text: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range x.Events() {
		if ev.Kind == harness.EventTextDelta {
			x.Cancel()
		}
	}
	if res, _ := x.Wait(); res.StopReason != "aborted" {
		t.Fatalf("stop = %q", res.StopReason)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close = %v, want no error", err)
	}
}

func TestPermissions(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{HarnessTools: []string{"Read"}})
	for tool, want := range map[string]string{"Read": "allow", "Bash": "deny", toolPrefix + "echo": "allow"} {
		if _, res, _ := exchange(t, s, harness.Request{Text: "permit:" + tool}); res.Text != want {
			t.Errorf("%s: %q, want %q", tool, res.Text, want)
		}
	}
}

func TestLaunch(t *testing.T) {
	dir := t.TempDir()
	id := "0f7d8f43-0000-4000-8000-000000000001"
	skill := harness.Skill{Name: "motto", FS: fstest.MapFS{"SKILL.md": {Data: []byte("# motto")}}}
	open(t, fakeDriver(dir), harness.Options{Model: "haiku", HarnessTools: []string{}, Skills: []harness.Skill{skill}, SessionID: id})

	// A session Claude Code holds, in any project, is resumed.
	held := filepath.Join(dir, "projects", "-elsewhere")
	if err := os.MkdirAll(held, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(held, id+".jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	open(t, fakeDriver(dir), harness.Options{SessionID: id})

	launches := lines[[]string](t, filepath.Join(dir, "launch"))
	first, second := launches[0], launches[1]
	flag := func(args []string, name string) (string, bool) {
		i := slices.Index(args, name)
		if i < 0 || i+1 >= len(args) {
			return "", false
		}
		return args[i+1], true
	}
	for _, f := range []string{"--setting-sources", "--strict-mcp-config", "--mcp-config", "--input-format", "--output-format"} {
		if !slices.Contains(first, f) {
			t.Errorf("launch lacks %s: %v", f, first)
		}
	}
	if v, _ := flag(first, "--tools"); v != "" || !slices.Contains(first, "--tools") {
		t.Errorf("--tools = %q, want none for an empty HarnessTools", v)
	}
	if v, _ := flag(first, "--session-id"); v != id {
		t.Errorf("--session-id = %q, want %s", v, id)
	}
	if v, _ := flag(first, "--model"); v != "haiku" {
		t.Errorf("--model = %q", v)
	}
	if plugin, ok := flag(first, "--plugin-dir"); !ok {
		t.Error("no --plugin-dir for the skill")
	} else if _, err := os.Stat(filepath.Join(plugin, "skills", "motto", "SKILL.md")); err != nil {
		t.Errorf("the plugin lacks the skill: %v", err)
	}
	if v, _ := flag(second, "--resume"); v != id || slices.Contains(second, "--tools") || slices.Contains(second, "--plugin-dir") {
		t.Errorf("second launch = %v, want --resume %s with the default tools and no plugin", second, id)
	}
}

func TestOpenRefuses(t *testing.T) {
	d := fakeDriver(t.TempDir())
	if _, err := d.Open(t.Context(), harness.Options{Provider: "llama.cpp"}); err == nil {
		t.Error("Open on another provider succeeded")
	}
	if _, err := d.Open(t.Context(), harness.Options{SessionID: "not-a-uuid"}); err == nil {
		t.Error("Open with a session ID that isn't a UUID succeeded")
	}
}

func TestImagesAreContentBlocks(t *testing.T) {
	dir := t.TempDir()
	s := open(t, fakeDriver(dir), harness.Options{})
	if _, _, err := exchange(t, s, harness.Request{Text: "look", Images: []harness.Image{{MediaType: "image/png", Data: []byte{1, 2, 3}}}}); err != nil {
		t.Fatal(err)
	}
	blocks := lines[[]contentBlock](t, filepath.Join(dir, "prompts"))[0]
	if len(blocks) != 2 || blocks[0].Type != "image" || blocks[0].Source.Data != "AQID" || blocks[0].Source.MediaType != "image/png" || blocks[1].Text != "look" {
		t.Fatalf("content = %+v", blocks)
	}
}
