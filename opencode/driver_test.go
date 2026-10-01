package opencode

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

const fakeProvider = "router"

// fakeDriver returns a Driver whose OpenCode is this test binary in fake mode, keeping its
// state and its launch record in dir.
func fakeDriver(dir string) Driver {
	return Driver{
		Command:  os.Args[0],
		StateDir: filepath.Join(dir, "state"),
		Providers: map[string]Provider{fakeProvider: {BaseURL: "http://router.invalid/v1",
			Headers: map[string]string{"Authorization": "Bearer t"},
			Models:  map[string]Model{"vision": {Image: true, Context: 8192}}}},
		// A race-enabled binary sleeps a second at exit unless told not to.
		Env: []string{fakeEnv + "=1", "GORACE=atexit_sleep_ms=0", launchEnv + "=" + filepath.Join(dir, "launch")},
	}
}

func open(t *testing.T, d Driver, opts harness.Options) *harness.Session {
	t.Helper()
	if opts.Provider == "" {
		opts.Provider = fakeProvider
	}
	s, err := d.Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

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

func TestAnExchange(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{})
	events, res, err := exchange(t, s, harness.Request{Text: "hi"})
	if err != nil || res.Text != "hello" || res.StopReason != "end_turn" || res.Usage != (harness.Usage{Input: 3, Output: 2, CacheRead: 5}) {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if events[0].Kind != harness.EventStarted || events[len(events)-1].Kind != harness.EventEnded {
		t.Errorf("events run %s … %s", events[0].Kind, events[len(events)-1].Kind)
	}
}

func TestToolsRunThroughTheDriversServer(t *testing.T) {
	echo := harness.Tool{Name: "echo", Description: "echo",
		Schema:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Handler: func(_ context.Context, args json.RawMessage) (string, error) { return "ECHO " + string(args), nil }}
	s := open(t, fakeDriver(t.TempDir()), harness.Options{Tools: []harness.Tool{echo}})
	events, res, err := exchange(t, s, harness.Request{Text: `tool:echo:{"text":"seven"}`})
	if err != nil || res.Text != `ECHO {"text":"seven"}` {
		t.Fatalf("result = %+v, %v", res, err)
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
}

func TestAStructuredResponsePerSchema(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{})
	city := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	count := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`)
	if _, res, err := exchange(t, s, harness.Request{Text: `structured:{"city":"Paris"}`, Schema: city}); err != nil || string(res.Structured) != `{"city":"Paris"}` {
		t.Fatalf("first = %s, %v", res.Structured, err)
	}
	// The fake answers with the respond tool it listed last, so this passes only if the
	// prompt waited for the listing after the schema changed.
	if _, res, err := exchange(t, s, harness.Request{Text: `structured:{"count":3}`, Schema: count}); err != nil || string(res.Structured) != `{"count":3}` {
		t.Fatalf("second = %s, %v", res.Structured, err)
	}
	events, _, err := exchange(t, s, harness.Request{Text: `structured:{"count":"three"}`, Schema: count})
	if !errors.Is(err, harness.ErrNoStructuredResponse) {
		t.Fatalf("error = %v, want ErrNoStructuredResponse", err)
	}
	if !slices.ContainsFunc(events, func(ev harness.Event) bool { return ev.Kind == harness.EventStructuredRejected }) {
		t.Error("no rejection")
	}
}

func TestCancel(t *testing.T) {
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
	if res, err := x.Wait(); err != nil || res.StopReason != "aborted" {
		t.Fatalf("result = %+v, %v; want aborted", res, err)
	}
	if _, res, err := exchange(t, s, harness.Request{Text: "hi"}); err != nil || res.Text != "hello" {
		t.Fatalf("the next exchange = %+v, %v", res, err)
	}
}

func TestPermissions(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{HarnessTools: []string{"read"}})
	for tool, want := range map[string]string{"read": "once", "bash": "reject", toolPrefix + "echo": "once"} {
		if _, res, _ := exchange(t, s, harness.Request{Text: "permit:" + tool}); res.Text != want {
			t.Errorf("%s: %q, want %q", tool, res.Text, want)
		}
	}
	open := open(t, fakeDriver(t.TempDir()), harness.Options{})
	if _, res, _ := exchange(t, open, harness.Request{Text: "permit:bash"}); res.Text != "once" {
		t.Errorf("with no allowlist, bash: %q, want once", res.Text)
	}
}

func TestImages(t *testing.T) {
	s := open(t, fakeDriver(t.TempDir()), harness.Options{})
	_, res, err := exchange(t, s, harness.Request{Text: "image", Images: []harness.Image{{MediaType: "image/png", Data: []byte{1, 2, 3}}}})
	if err != nil || res.Text != "image/png:AQID" {
		t.Fatalf("result = %q, %v", res.Text, err)
	}
}

func TestResume(t *testing.T) {
	dir := t.TempDir()
	d := fakeDriver(dir)
	first := open(t, d, harness.Options{})
	id := first.ID()
	_ = first.Close()

	s := open(t, d, harness.Options{SessionID: id})
	if s.ID() != id {
		t.Fatalf("resumed %s, want %s", s.ID(), id)
	}
	// The replayed history belongs to no exchange.
	events, res, err := exchange(t, s, harness.Request{Text: "hi"})
	if err != nil || res.Text != "hello" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	for _, ev := range events {
		if ev.Kind == harness.EventTextDelta && ev.Text == "reply" {
			t.Fatal("the replayed history reached the exchange")
		}
	}

	// OpenCode can't create a session under a chosen ID, so an ID it doesn't hold fails.
	if _, err := d.Open(t.Context(), harness.Options{Provider: fakeProvider, SessionID: "ses_unknown"}); err == nil {
		t.Fatal("Open on an unknown session succeeded")
	}
}

func TestLaunchConfig(t *testing.T) {
	dir := t.TempDir()
	skill := harness.Skill{Name: "motto", FS: fstest.MapFS{"SKILL.md": {Data: []byte("# motto")}}}
	d := fakeDriver(dir)
	d.CacheDir = filepath.Join(dir, "cache")
	open(t, d, harness.Options{Model: "chat", HarnessTools: []string{"skill"}, Skills: []harness.Skill{skill}})

	f, err := os.Open(filepath.Join(dir, "launch"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var launch struct {
		Args   []string `json:"args"`
		Config struct {
			Enabled  []string `json:"enabled_providers"`
			Provider map[string]struct {
				NPM     string `json:"npm"`
				Options struct {
					BaseURL string            `json:"baseURL"`
					Headers map[string]string `json:"headers"`
				} `json:"options"`
				Models map[string]struct {
					Modalities struct {
						Input []string `json:"input"`
					} `json:"modalities"`
				} `json:"models"`
			} `json:"provider"`
			Skills struct {
				Paths []string `json:"paths"`
			} `json:"skills"`
			Tools map[string]bool `json:"tools"`
		} `json:"config"`
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	sc.Scan()
	if err := json.Unmarshal(sc.Bytes(), &launch); err != nil {
		t.Fatal(err)
	}
	c := launch.Config
	if !slices.Equal(launch.Args, []string{"acp", "--pure"}) || !slices.Equal(c.Enabled, []string{fakeProvider}) {
		t.Errorf("args %v, enabled %v", launch.Args, c.Enabled)
	}
	p := c.Provider[fakeProvider]
	if p.NPM != "@ai-sdk/openai-compatible" || p.Options.BaseURL != "http://router.invalid/v1" || p.Options.Headers["Authorization"] != "Bearer t" {
		t.Errorf("provider = %+v", p)
	}
	if !slices.Equal(p.Models["vision"].Modalities.Input, []string{"text", "image"}) || !slices.Equal(p.Models["chat"].Modalities.Input, []string{"text"}) {
		t.Errorf("models = %+v, want the session's model listed as text only beside the known one", p.Models)
	}
	if len(c.Skills.Paths) != 1 || !strings.HasPrefix(c.Skills.Paths[0], d.CacheDir) {
		t.Errorf("skill paths = %v, want the cached skill", c.Skills.Paths)
	} else if _, err := os.Stat(filepath.Join(c.Skills.Paths[0], "SKILL.md")); err != nil {
		t.Errorf("the cached skill: %v", err)
	}
	want := map[string]bool{"*": false, toolPrefix + "*": true, "skill": true}
	if len(c.Tools) != len(want) {
		t.Errorf("tools = %v, want %v", c.Tools, want)
	}
	for k, v := range want {
		if c.Tools[k] != v {
			t.Errorf("tools[%q] = %v, want %v", k, c.Tools[k], v)
		}
	}
}

func TestOpenRefusesAnUnknownProvider(t *testing.T) {
	if _, err := fakeDriver(t.TempDir()).Open(t.Context(), harness.Options{Provider: "elsewhere"}); err == nil {
		t.Fatal("Open on an unknown provider succeeded")
	}
}

// An endpoint serving reasoning models takes @ai-sdk/openai, with a token as its API key.
func TestAProvidersPackageAndKey(t *testing.T) {
	d := fakeDriver(t.TempDir())
	d.Providers["azure"] = Provider{NPM: "@ai-sdk/openai", BaseURL: "https://example.invalid/openai/v1", APIKey: "token"}
	cfg, err := d.config(harness.Options{Provider: "azure", Model: "gpt-5-mini"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Provider map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				APIKey  string            `json:"apiKey"`
				Headers map[string]string `json:"headers"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	p := c.Provider["azure"]
	if p.NPM != "@ai-sdk/openai" || p.Options.APIKey != "token" || p.Options.Headers != nil {
		t.Fatalf("provider = %+v", p)
	}
	if _, others := c.Provider[fakeProvider]; others {
		t.Error("the configuration enables a provider the session doesn't run on")
	}
}
