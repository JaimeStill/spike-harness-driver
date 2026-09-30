package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/model"
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
		{nil, "unsloth/Qwen3.8-27B-GGUF:Q4_K_XL", "Qwen/Qwen3-Embedding-4B-GGUF:Q5_K_M", "ggml-org/gemma-4-E4B-it-GGUF:Q8_0", true},
		{[]string{"--target", "azure"}, "gpt-5-mini", "text-embedding-3-small", "gpt-4o-mini-transcribe", false},
		{
			[]string{"--target", "azure", "--vision-model", "gpt-6-luna", "--audio-model", "gpt-transcribe"},
			"gpt-6-luna", "text-embedding-3-small", "gpt-transcribe", false,
		},
		{[]string{"--embed-model", "e"}, "unsloth/Qwen3.8-27B-GGUF:Q4_K_XL", "e", "ggml-org/gemma-4-E4B-it-GGUF:Q8_0", true},
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
		if m.HarnessVision != "unsloth/Qwen3.8-27B-GGUF:Q4_K_XL" || m.Chat == nil || m.Audio == nil {
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
