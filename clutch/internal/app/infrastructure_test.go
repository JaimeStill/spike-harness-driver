package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/model"
	"github.com/JaimeStill/spike-harness-driver/opencode"
	"github.com/JaimeStill/spike-harness-driver/pi"
)

func TestValidateLoadsTheSourcesIntoEverySessionsOptions(t *testing.T) {
	infra := newInfrastructure(&Config{
		Harness: "pi",
		Target:  "llama.cpp",
		State:   t.TempDir(),
		Tools:   []string{"../../examples/tools"},
		Skills:  []string{"../../examples/skills"},
	})
	if opts := infra.Options(); len(opts.Tools) != 0 || len(opts.Skills) != 0 {
		t.Fatalf("options before Validate: %+v", opts)
	}
	if err := infra.Validate(); err != nil {
		t.Fatal(err)
	}
	opts := infra.Options()
	if len(opts.Tools) != 1 || opts.Tools[0].Name != "fingerprint" || opts.Tools[0].Handler == nil {
		t.Errorf("tools = %+v", opts.Tools)
	}
	if len(opts.Skills) != 1 || opts.Skills[0].Name != "clutch-weather" {
		t.Errorf("skills = %+v", opts.Skills)
	}
	// The session domain reads the same options, so session send offers them too.
	svc := newDomain(infra).Session
	if len(svc.Tools()) != 1 || len(svc.Skills()) != 1 {
		t.Errorf("session domain offers %d tools, %d skills", len(svc.Tools()), len(svc.Skills()))
	}
}

func TestTheExampleToolRuns(t *testing.T) {
	infra := newInfrastructure(&Config{Harness: "pi", Target: "llama.cpp", Tools: []string{"../../examples/tools"}})
	if err := infra.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := infra.Options().Tools[0].Handler(t.Context(), []byte(`{"text":"clutch"}`))
	if err != nil {
		t.Skipf("fingerprint needs python3: %v", err)
	}
	if got != "c278be9e2247\n" {
		t.Errorf("fingerprint(clutch) = %q", got)
	}
}

// flagged returns a Config with every flag at its default, then set as args give them.
func flagged(t *testing.T, args ...string) *Config {
	t.Helper()
	cfg := &Config{}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	cfg.bind(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestValidateChecksTheTarget(t *testing.T) {
	for _, target := range []string{"llama.cpp", "azure"} {
		if err := newInfrastructure(flagged(t, "--target", target)).Validate(); err != nil {
			t.Errorf("--target %s: %v", target, err)
		}
	}
	err := newInfrastructure(flagged(t, "--target", "openai")).Validate()
	if err == nil || err.Error() != `unknown target "openai" (known: llama.cpp, azure)` {
		t.Errorf("Validate = %v", err)
	}
	if _, err := newInfrastructure(flagged(t, "--target", "openai")).Models(); err == nil {
		t.Error("Models built clients for an unknown target")
	}
}

func TestModelsTakeTheTargetsDefaultsUnlessOverridden(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "http://router.test:8080/")
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://acct.openai.azure.com/openai/v1")
	cases := []struct {
		args                 []string
		vision, embed, audio string
		audioInChat          bool
	}{
		{nil, "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0", "ggml-org/embeddinggemma-2-GGUF:Q8_0", "ggml-org/gemma-4-E4B-it-GGUF:Q8_0", true},
		{[]string{"--target", "azure"}, "gpt-5-mini", "text-embedding-3-small", "gpt-4o-mini-transcribe", false},
		{
			[]string{"--target", "azure", "--vision-model", "gpt-6-luna", "--audio-model", "gpt-transcribe"},
			"gpt-6-luna", "text-embedding-3-small", "gpt-transcribe", false,
		},
		{[]string{"--embed-model", "e"}, "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0", "e", "ggml-org/gemma-4-E4B-it-GGUF:Q8_0", true},
	}
	for _, c := range cases {
		m, err := newInfrastructure(flagged(t, c.args...)).Models()
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if m.VisionModel != c.vision || m.EmbedModel != c.embed || m.AudioModel != c.audio || m.AudioInChat != c.audioInChat {
			t.Errorf("%v: %+v", c.args, m)
		}
		if m.HarnessVision != "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0" || m.Chat == nil || m.Audio == nil {
			t.Errorf("%v: %+v", c.args, m)
		}
		if (m.Target == "llama.cpp") != (m.Chat == m.Audio) {
			t.Errorf("%v: target %s shares its audio client: %v", c.args, m.Target, m.Chat == m.Audio)
		}
	}
	m, err := newInfrastructure(flagged(t, "--harness-vision-model", "h")).Models()
	if err != nil || m.HarnessVision != "h" {
		t.Errorf("Models = %+v, %v", m, err)
	}
}

func TestHarnessDefaultsFollowTheHarnessUnlessOverridden(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "http://router.test:8080/")
	cases := []struct {
		args                  []string
		provider, model, wide string
		skillTool             string
	}{
		{nil, "llama.cpp", "ggml-org/gpt-oss-120b-GGUF:MXFP4", "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0", "read"},
		{[]string{"--harness", "pi"}, "llama.cpp", "ggml-org/gpt-oss-120b-GGUF:MXFP4", "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0", "read"},
		{[]string{"--harness", "claude"}, "anthropic", "haiku", "haiku", "Skill"},
		{[]string{"--harness", "claude", "--model", "sonnet"}, "anthropic", "sonnet", "haiku", "Skill"},
		{[]string{"--harness", "claude", "--harness-vision-model", "opus"}, "anthropic", "haiku", "opus", "Skill"},
		{[]string{"--provider", "p", "--model", "m"}, "p", "m", "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0", "read"},
	}
	for _, c := range cases {
		infra := newInfrastructure(flagged(t, c.args...))
		opts := infra.Options()
		if opts.Provider != c.provider || opts.Model != c.model {
			t.Errorf("%v: options %q on %q, want %q on %q", c.args, opts.Model, opts.Provider, c.model, c.provider)
		}
		m, err := infra.Models()
		if err != nil || m.HarnessVision != c.wide {
			t.Errorf("%v: HarnessVision = %q, %v, want %q", c.args, m.HarnessVision, err, c.wide)
		}
		if got := infra.Profile().SkillTool; got != c.skillTool {
			t.Errorf("%v: skill tool %q, want %q", c.args, got, c.skillTool)
		}
	}
}

func TestDriverIsBuiltPerHarness(t *testing.T) {
	for name, want := range map[string]string{"pi": "pi.Driver", "claude": "claude.Driver", "opencode": "opencode.Driver"} {
		d, err := newInfrastructure(flagged(t, "--harness", name, "--state", t.TempDir())).Driver()
		if err != nil || fmt.Sprintf("%T", d) != want {
			t.Errorf("--harness %s: Driver = %T, %v, want %s", name, d, err, want)
		}
	}
	if _, err := newInfrastructure(flagged(t, "--harness", "nope")).Driver(); err == nil {
		t.Error("Driver built an adapter for an unknown harness")
	}
}

func TestOpenCodesProviderIsTheRouter(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "http://router.invalid:8080/")
	d, err := newInfrastructure(flagged(t, "--harness", "opencode", "--harness-vision-model", "seer")).Driver()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := d.(opencode.Driver).Providers[providerLlama]
	if !ok || p.BaseURL != "http://router.invalid:8080/v1" || !p.Models["seer"].Image {
		t.Fatalf("provider = %+v, want the router's /v1 base with the vision model taking images", p)
	}
}

func TestNeedsLooksUpTheHarnessExecutable(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, ok := range map[string]bool{"claude": true, "pi": false} {
		err := newInfrastructure(flagged(t, "--harness", name)).Needs().Harness[0].Check(t.Context())
		if (err == nil) != ok {
			t.Errorf("--harness %s: %v", name, err)
		}
	}
}

func TestModelsNeedTheTargetsBaseURL(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	for _, target := range []string{"llama.cpp", "azure"} {
		if _, err := newInfrastructure(flagged(t, "--target", target)).Models(); err == nil || !strings.Contains(err.Error(), "is not set") {
			t.Errorf("--target %s: Models = %v", target, err)
		}
	}
}

func TestTheTargetsNeeds(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no az, no harness
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	failing := func(needs []scenario.Need) []string {
		var failed []string
		for _, n := range needs {
			if n.Check(t.Context()) != nil {
				failed = append(failed, n.What)
			}
		}
		return failed
	}
	llama := newInfrastructure(flagged(t)).Needs()
	if got := failing(llama.Models); !slices.Equal(got, []string{"LLAMA_BASE_URL set when the target is llama.cpp"}) {
		t.Errorf("llama.cpp target: failing %v", got)
	}
	// The provider and the target share the one need.
	if got := failing(llama.Both); !slices.Equal(got, []string{
		"the harness executable on the PATH", "LLAMA_BASE_URL set when the provider or the target is llama.cpp",
	}) {
		t.Errorf("llama.cpp target, both: failing %v", got)
	}
	azure := newInfrastructure(flagged(t, "--target", "azure")).Needs()
	if got := failing(azure.Models); !slices.Equal(got, []string{
		"AZURE_OPENAI_BASE_URL set when the target is azure", "the Azure CLI, az, on the PATH when the target is azure",
	}) {
		t.Errorf("azure target: failing %v", got)
	}
	// The harness still talks to the router, so its provider still needs LLAMA_BASE_URL.
	if got := failing(azure.Both); len(got) != 4 || got[1] != "LLAMA_BASE_URL set when the provider or the target is llama.cpp" {
		t.Errorf("azure target, both: failing %v", got)
	}
	if got := failing(azure.Harness); len(got) != 2 {
		t.Errorf("azure target, harness: failing %v", got)
	}
}

func TestAzureAudioBase(t *testing.T) {
	for _, base := range []string{"https://acct.openai.azure.com/openai/v1", "https://acct.openai.azure.com/openai/v1/"} {
		got, err := azureAudioBase(base, "gpt-4o-mini-transcribe")
		if err != nil || got != "https://acct.openai.azure.com/openai/deployments/gpt-4o-mini-transcribe" {
			t.Errorf("azureAudioBase(%q) = %q, %v", base, got, err)
		}
	}
	if _, err := azureAudioBase("https://acct.openai.azure.com/openai", "d"); err == nil {
		t.Error("a base URL without /v1 was accepted")
	}
}

// fakeAz is an az that prints tokens in turn, each expiring at its expiry, and counts its runs.
type fakeAz struct {
	mu      sync.Mutex
	runs    int
	args    []string
	expires time.Time
}

func (f *fakeAz) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	f.args = args
	return []byte(`{"accessToken":"token-` + strconv.Itoa(f.runs) + `","expires_on":` +
		strconv.FormatInt(f.expires.Unix(), 10) + `,"tokenType":"Bearer"}`), nil
}

func TestTheAzureClientsRouteAndAuthenticate(t *testing.T) {
	type seen struct{ path, query, auth string }
	var (
		mu   sync.Mutex
		reqs []seen
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		reqs = append(reqs, seen{r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			_, _ = io.WriteString(w, `{"text":"t"}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"c"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	t.Setenv("AZURE_OPENAI_BASE_URL", srv.URL+"/openai/v1")

	az := &fakeAz{expires: time.Now().Add(time.Hour)}
	infra := newInfrastructure(flagged(t, "--target", "azure", "--azure-scope", "https://scope.test"))
	infra.az = az.run
	m, err := infra.Models()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Chat.Chat(t.Context(), model.ChatRequest{
		Model:    m.VisionModel,
		Messages: []model.Message{{Role: model.RoleUser, Parts: []model.Part{model.Text{Text: "hi"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Audio.Transcribe(t.Context(), model.TranscribeRequest{
		Model: m.AudioModel, Audio: strings.NewReader("RIFF"), Filename: "phrase.wav",
	}); err != nil {
		t.Fatal(err)
	}
	want := []seen{
		{"/openai/v1/chat/completions", "", "Bearer token-1"},
		{"/openai/deployments/gpt-4o-mini-transcribe/audio/transcriptions", "api-version=2025-04-01-preview", "Bearer token-1"},
	}
	if !slices.Equal(reqs, want) {
		t.Errorf("requests = %+v\nwant %+v", reqs, want)
	}
	// Both clients share the one cached token: az ran once, for the scope the flag names.
	if az.runs != 1 || !slices.Equal(az.args, []string{"account", "get-access-token", "--resource", "https://scope.test", "-o", "json"}) {
		t.Errorf("az ran %d times, last with %v", az.runs, az.args)
	}
}

func TestTheAzureTokenIsCachedUntilNearItsExpiry(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	az := &fakeAz{expires: now.Add(time.Hour)}
	tok := &azureToken{scope: "s", run: az.run, now: func() time.Time { return now }}
	get := func() string {
		t.Helper()
		got, err := tok.Token(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := get(); got != "token-1" {
		t.Errorf("first token = %q", got)
	}
	now = now.Add(time.Hour - tokenMargin - time.Second)
	if got := get(); got != "token-1" || az.runs != 1 {
		t.Errorf("before the margin: %q after %d runs", got, az.runs)
	}
	az.expires = now.Add(time.Hour)
	now = now.Add(2 * time.Second)
	if got := get(); got != "token-2" || az.runs != 2 {
		t.Errorf("within the margin: %q after %d runs", got, az.runs)
	}
	if got := get(); got != "token-2" || az.runs != 2 {
		t.Errorf("after the refresh: %q after %d runs", got, az.runs)
	}
}

func TestAnAzFailureIsNotCached(t *testing.T) {
	boom := errors.New("exit status 1: Please run 'az login'")
	fail := true
	tok := &azureToken{scope: "s", now: time.Now, run: func(context.Context, ...string) ([]byte, error) {
		if fail {
			return nil, boom
		}
		return []byte(`{"accessToken":"t","expires_on":"` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `"}`), nil
	}}
	if _, err := tok.Token(t.Context()); !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "az account get-access-token: ") {
		t.Errorf("Token = %v", err)
	}
	fail = false
	if got, err := tok.Token(t.Context()); err != nil || got != "t" {
		t.Errorf("Token = %q, %v", got, err)
	}
}

func TestParseAzToken(t *testing.T) {
	want := time.Unix(1790000000, 0)
	local := time.Date(2026, 9, 30, 13, 4, 5, 0, time.Local)
	cases := []struct {
		out     string
		expires time.Time
	}{
		{`{"accessToken":"secret","expires_on":1790000000}`, want},
		{`{"accessToken":"secret","expires_on":"1790000000"}`, want},
		{`{"accessToken":"secret","expires_on":1790000000,"expiresOn":"2000-01-01 00:00:00.000000"}`, want},
		{`{"accessToken":"secret","expiresOn":"2026-09-30 13:04:05.000000"}`, local},
		{`{"accessToken":"secret","expiresOn":"2026-09-30 13:04:05"}`, local},
	}
	for _, c := range cases {
		token, expires, err := parseAzToken([]byte(c.out))
		if err != nil || token != "secret" || !expires.Equal(c.expires) {
			t.Errorf("%s: %q, %v, %v", c.out, token, expires, err)
		}
	}
	for _, out := range []string{
		`not json secret`,
		`{"expires_on":1790000000}`,
		`{"accessToken":"secret"}`,
		`{"accessToken":"secret","expires_on":"soon"}`,
		`{"accessToken":"secret","expiresOn":"tomorrow"}`,
	} {
		_, _, err := parseAzToken([]byte(out))
		if err == nil {
			t.Errorf("%s parsed", out)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the error quotes the token: %v", out, err)
		}
	}
}

// On the azure provider, Pi and OpenCode default to gpt-5-mini; Pi's key is the az command
// in its own agent directory, and OpenCode's a token, through @ai-sdk/openai.
func TestTheAzureProvider(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://example.invalid/openai/v1")
	state := t.TempDir()

	infra := newInfrastructure(flagged(t, "--harness", "pi", "--provider", "azure", "--state", state, "--azure-scope", "https://scope.test"))
	if err := infra.Validate(); err != nil {
		t.Fatal(err)
	}
	if o := infra.Options(); o.Provider != "azure" || o.Model != "gpt-5-mini" {
		t.Errorf("options = %s %s, want azure gpt-5-mini", o.Provider, o.Model)
	}
	if m, err := infra.Models(); err != nil || m.HarnessVision != "gpt-5-mini" || !m.DefaultTakesImages {
		t.Errorf("models = %+v, %v", m, err)
	}
	d, err := infra.Driver()
	if err != nil {
		t.Fatal(err)
	}
	p := d.(pi.Driver)
	key := p.Providers["azure"].APIKey
	if p.AgentDir != filepath.Join(state, "pi-agent") || key != "!az account get-access-token --resource https://scope.test --query accessToken -o tsv" {
		t.Errorf("pi driver = %s, key %q", p.AgentDir, key)
	}

	az := &fakeAz{expires: time.Now().Add(time.Hour)}
	infra = newInfrastructure(flagged(t, "--harness", "opencode", "--provider", "azure", "--state", state, "--azure-scope", "https://scope.test"))
	infra.az = az.run
	d, err = infra.Driver()
	if err != nil {
		t.Fatal(err)
	}
	oc := d.(opencode.Driver).Providers["azure"]
	if oc.NPM != "@ai-sdk/openai" || oc.APIKey == "" || !oc.Models["gpt-5-mini"].Image {
		t.Errorf("opencode provider = %+v", oc)
	}

	// A harness vision model other than the session's is listed too, taking images, so a
	// session can select it.
	vision := "gpt-5-vision"
	infra = newInfrastructure(flagged(t, "--harness", "pi", "--provider", "azure", "--state", state,
		"--azure-scope", "https://scope.test", "--harness-vision-model", vision))
	if d, err = infra.Driver(); err != nil {
		t.Fatal(err)
	}
	var piModels []string
	for _, m := range d.(pi.Driver).Providers["azure"].Models {
		if m.Image {
			piModels = append(piModels, m.ID)
		}
	}
	if !slices.Equal(piModels, []string{"gpt-5-mini", vision}) {
		t.Errorf("pi's azure models taking images = %v, want the session's and the vision model", piModels)
	}
	infra = newInfrastructure(flagged(t, "--harness", "opencode", "--provider", "azure", "--state", state,
		"--azure-scope", "https://scope.test", "--harness-vision-model", vision))
	infra.az = az.run
	if d, err = infra.Driver(); err != nil {
		t.Fatal(err)
	}
	if ms := d.(opencode.Driver).Providers["azure"].Models; len(ms) != 2 || !ms["gpt-5-mini"].Image || !ms[vision].Image {
		t.Errorf("opencode's azure models = %+v, want the session's and the vision model, taking images", ms)
	}

	// The scope reaches a command line, so one a shell would read is refused.
	bad := newInfrastructure(flagged(t, "--harness", "pi", "--provider", "azure", "--azure-scope", "https://x; rm -rf ~"))
	if err := bad.Validate(); err == nil {
		t.Error("Validate took a scope a shell would read")
	}
}
