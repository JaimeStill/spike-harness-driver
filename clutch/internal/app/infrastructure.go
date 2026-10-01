package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/catalog"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/model"
)

// modelTimeout bounds one direct model request, from sending it to reading its response. The
// router loads a model on its first request, which for the 27B vision model takes a minute or
// more before a reasoning model's answer starts. The bound is generous because it exists to
// catch an endpoint that has hung, not one that is slow.
const modelTimeout = 10 * time.Minute

// Infrastructure resolves the harness the flags name, the store that keeps exchange records,
// both under the --state directory, the skills and command tools the --skills and --tools
// directories hold, and the direct model clients for the --target endpoint. It opens nothing
// itself: each session the domain opens starts, and ends, its own harness process, and each
// model request makes its own connection.
type Infrastructure struct {
	cfg *Config
	// tools and skills are what Validate loaded from the --tools and --skills directories.
	tools  []harness.Tool
	skills []harness.Skill
	// http sends every direct model request, whichever client makes it.
	http *http.Client
	// az runs the Azure CLI for the azure target's tokens; a test replaces it.
	az azRunner

	tokenOnce sync.Once
	token     *azureToken
}

func newInfrastructure(cfg *Config) *Infrastructure {
	return &Infrastructure{cfg: cfg, http: &http.Client{Timeout: modelTimeout}, az: runAz}
}

// Validate fails when the flags name a harness clutch has no adapter for, a target it has no
// client setup for, or a --skills or --tools directory that doesn't load. It keeps what the
// directories hold for Options, so a bad directory fails the command before any harness starts
// rather than when a session opens.
func (i *Infrastructure) Validate() error {
	if _, err := lookupHarness(i.cfg.Harness); err != nil {
		return err
	}
	if _, err := lookupTarget(i.cfg.Target); err != nil {
		return err
	}
	skills, err := loadSkills(i.cfg.Skills)
	if err != nil {
		return err
	}
	tools, err := loadTools(i.cfg.Tools)
	if err != nil {
		return err
	}
	i.skills, i.tools = skills, tools
	return nil
}

// loadSkills loads the skills under each directory. A directory holding none is an error, as
// is a name two directories share: either is more likely a mistyped flag than intended.
func loadSkills(dirs []string) ([]harness.Skill, error) {
	var all []harness.Skill
	where := map[string]string{}
	for _, dir := range dirs {
		// Loaded with their directories, so the harness reads each skill where it lives.
		skills, err := catalog.SkillsDir(dir)
		if err != nil {
			return nil, fmt.Errorf("--skills %s: %w", dir, err)
		}
		if len(skills) == 0 {
			return nil, fmt.Errorf("--skills %s: no skills: no subdirectory holds a SKILL.md", dir)
		}
		for _, s := range skills {
			if prev, ok := where[s.Name]; ok {
				return nil, fmt.Errorf("--skills %s: skill %q is also in %s", dir, s.Name, prev)
			}
			where[s.Name] = dir
		}
		all = append(all, skills...)
	}
	return all, nil
}

// loadTools loads the command tools under each directory, on the same terms as loadSkills.
func loadTools(dirs []string) ([]harness.Tool, error) {
	var all []harness.Tool
	where := map[string]string{}
	for _, dir := range dirs {
		tools, err := catalog.Tools(dir)
		if err != nil {
			return nil, fmt.Errorf("--tools %s: %w", dir, err)
		}
		if len(tools) == 0 {
			return nil, fmt.Errorf("--tools %s: no command tools: no subdirectory holds a tool.json", dir)
		}
		for _, t := range tools {
			if prev, ok := where[t.Name]; ok {
				return nil, fmt.Errorf("--tools %s: tool %q is also in %s", dir, t.Name, prev)
			}
			where[t.Name] = dir
		}
		all = append(all, tools...)
	}
	return all, nil
}

// Driver returns the adapter for the harness the flags name.
func (i *Infrastructure) Driver() (harness.Driver, error) {
	h, err := lookupHarness(i.cfg.Harness)
	if err != nil {
		return nil, err
	}
	return h.driver(i.cfg.State), nil
}

// spec returns the harness the flags name. Validate has already failed for a name with no
// adapter, so an unknown one gives the zero harnessSpec, whose empty defaults nothing reads.
func (i *Infrastructure) spec() harnessSpec {
	h, _ := lookupHarness(i.cfg.Harness)
	return h
}

// provider is the harness's model provider: --provider, or the harness's own.
func (i *Infrastructure) provider() string { return cmp.Or(i.cfg.Provider, i.spec().provider) }

// Profile returns how the scenarios talk to the harness the flags name.
func (i *Infrastructure) Profile() scenario.Profile { return i.spec().profile }

// Options returns the session options the flags name, with the store that keeps exchange
// records and the skills and tools Validate loaded.
func (i *Infrastructure) Options() harness.Options {
	return harness.Options{
		Provider: i.provider(),
		Model:    cmp.Or(i.cfg.Model, i.spec().model),
		Store:    i.Store(),
		Tools:    i.tools,
		Skills:   i.skills,
	}
}

// Store returns the store that keeps exchange records, in the --state directory.
func (i *Infrastructure) Store() harness.Store {
	return filestore.New(filepath.Join(i.cfg.State, "exchanges"))
}

// Needs returns what each kind of scenario checks first: what the harness requires to run,
// what the direct model client requires for the --target endpoint, and both together, with the
// LLAMA_BASE_URL need the two share listed once.
func (i *Infrastructure) Needs() scenario.Needs {
	executable := scenario.Need{
		What: "the harness executable on the PATH",
		Check: func(context.Context) error {
			_, err := exec.LookPath(i.spec().command)
			return err
		},
	}
	azure := []scenario.Need{
		{
			What: "AZURE_OPENAI_BASE_URL set when the target is azure",
			Check: func(context.Context) error {
				if i.cfg.Target == targetAzure && os.Getenv("AZURE_OPENAI_BASE_URL") == "" {
					return errors.New("AZURE_OPENAI_BASE_URL is not set")
				}
				return nil
			},
		},
		{
			What: "the Azure CLI, az, on the PATH when the target is azure",
			Check: func(context.Context) error {
				if i.cfg.Target != targetAzure {
					return nil
				}
				_, err := exec.LookPath("az")
				return err
			},
		},
	}
	return scenario.Needs{
		Harness: []scenario.Need{executable, i.llamaNeed(true, false)},
		Models:  append([]scenario.Need{i.llamaNeed(false, true)}, azure...),
		Both:    append([]scenario.Need{executable, i.llamaNeed(true, true)}, azure...),
	}
}

// llamaNeed is the need for LLAMA_BASE_URL. With provider set, it applies when the harness's
// provider is llama.cpp. With target set, it applies when the target is. With both set, it
// applies when either is.
func (i *Infrastructure) llamaNeed(provider, target bool) scenario.Need {
	var when []string
	if provider {
		when = append(when, "the provider")
	}
	if target {
		when = append(when, "the target")
	}
	return scenario.Need{
		What: "LLAMA_BASE_URL set when " + strings.Join(when, " or ") + " is llama.cpp",
		Check: func(context.Context) error {
			used := (provider && i.provider() == providerLlama) || (target && i.cfg.Target == targetLlama)
			if used && os.Getenv("LLAMA_BASE_URL") == "" {
				return errors.New("LLAMA_BASE_URL is not set")
			}
			return nil
		},
	}
}

// The targets the direct model client talks to.
const (
	targetLlama = "llama.cpp"
	targetAzure = "azure"
)

// providerLlama is the harness's provider for the llama.cpp router, the same name the router's
// target has.
const providerLlama = "llama.cpp"

// qwenVision is the router's vision model: the llama.cpp target's, and a harness session's when
// it is sent an image.
const qwenVision = "unsloth/Qwen3.8-27B-GGUF:Q4_K_XL"

// target holds a target's name, its default model IDs, and whether its audio model takes audio
// in chat.
type target struct {
	name                 string
	vision, embed, audio string
	audioInChat          bool
}

// targets are the targets, in the order help and errors list them. setup/router-models.md and
// setup/azure-foundry.md say why each model was chosen.
var targets = []target{
	{
		name:        targetLlama,
		vision:      qwenVision,
		embed:       "Qwen/Qwen3-Embedding-4B-GGUF:Q5_K_M",
		audio:       "ggml-org/gemma-4-E4B-it-GGUF:Q8_0",
		audioInChat: true,
	},
	// Azure's transcription models take audio only through transcription, and its chat models
	// take none.
	{
		name:   targetAzure,
		vision: "gpt-5-mini",
		embed:  "text-embedding-3-small",
		audio:  "gpt-4o-mini-transcribe",
	},
}

// targetNames returns the targets' names, in the order help and errors list them.
func targetNames() []string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.name
	}
	return names
}

// lookupTarget returns the target named name.
func lookupTarget(name string) (target, error) {
	for _, t := range targets {
		if t.name == name {
			return t, nil
		}
	}
	return target{}, fmt.Errorf("unknown target %q (known: %s)", name, strings.Join(targetNames(), ", "))
}

// azureAudioVersion is the api-version the azure target's transcription route takes.
const azureAudioVersion = "2025-04-01-preview"

// Models returns the direct model clients for the --target endpoint, with the model IDs the
// flags name or the target's defaults. It reads the target's environment variables and makes no
// request.
func (i *Infrastructure) Models() (scenario.Models, error) {
	t, err := lookupTarget(i.cfg.Target)
	if err != nil {
		return scenario.Models{}, err
	}
	m := scenario.Models{
		Target:        i.cfg.Target,
		VisionModel:   cmp.Or(i.cfg.VisionModel, t.vision),
		EmbedModel:    cmp.Or(i.cfg.EmbedModel, t.embed),
		AudioModel:    cmp.Or(i.cfg.AudioModel, t.audio),
		AudioInChat:   t.audioInChat,
		HarnessVision: cmp.Or(i.cfg.HarnessVisionModel, i.spec().vision),
	}
	switch t.name {
	case targetLlama:
		base := os.Getenv("LLAMA_BASE_URL")
		if base == "" {
			return scenario.Models{}, errors.New("LLAMA_BASE_URL is not set")
		}
		// LLAMA_BASE_URL is the router's root, which Pi's provider takes. The client needs the
		// OpenAI-compatible API root beneath it, where the router serves every route.
		m.Chat, err = model.New(model.Config{BaseURL: strings.TrimSuffix(base, "/") + "/v1", HTTP: i.http})
		m.Audio = m.Chat
	case targetAzure:
		base := os.Getenv("AZURE_OPENAI_BASE_URL")
		if base == "" {
			return scenario.Models{}, errors.New("AZURE_OPENAI_BASE_URL is not set")
		}
		token := i.azureToken().Token
		if m.Chat, err = model.New(model.Config{BaseURL: base, Token: token, HTTP: i.http}); err != nil {
			return scenario.Models{}, err
		}
		// Azure's v1 /audio/transcriptions route answered 404 DeploymentNotFound for a
		// deployment that existed (checked 2026-09-30), while the older deployment route
		// works. Transcription therefore gets its own client on that route.
		audio, aerr := azureAudioBase(base, m.AudioModel)
		if aerr != nil {
			return scenario.Models{}, aerr
		}
		m.Audio, err = model.New(model.Config{
			BaseURL: audio,
			Query:   url.Values{"api-version": {azureAudioVersion}},
			Token:   token,
			HTTP:    i.http,
		})
	}
	if err != nil {
		return scenario.Models{}, err
	}
	return m, nil
}

// azureAudioBase is the deployment route's base URL for deployment: base, the v1 API root
// ending in /openai/v1, with /v1 replaced by /deployments/<deployment>.
func azureAudioBase(base, deployment string) (string, error) {
	root, ok := strings.CutSuffix(strings.TrimSuffix(base, "/"), "/v1")
	if !ok {
		return "", fmt.Errorf("AZURE_OPENAI_BASE_URL %q doesn't end in /v1", base)
	}
	return root + "/deployments/" + url.PathEscape(deployment), nil
}

// azureToken returns the Infrastructure's single token source for the azure target, so every
// client shares one cached token.
func (i *Infrastructure) azureToken() *azureToken {
	i.tokenOnce.Do(func() {
		i.token = &azureToken{scope: i.cfg.AzureScope, run: i.az, now: time.Now}
	})
	return i.token
}

// azRunner runs the Azure CLI with args and returns what it writes to standard output.
type azRunner func(ctx context.Context, args ...string) ([]byte, error)

// runAz runs az. A failure carries az's standard error, which says why, such as an expired
// sign-in. The token goes only to standard output.
func runAz(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "az", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// tokenMargin is how long before its expiry a cached token is replaced. Azure checks a token
// when a request arrives, so the margin need only cover the request's travel time and any skew
// between this machine's clock and Azure's. Five minutes is ample for both, and costs only a
// refresh a few minutes early on a token that lasts an hour or more.
const tokenMargin = 5 * time.Minute

// azureToken is a model.TokenSource for Entra ID tokens from the Azure CLI's sign-in. It runs
// az account get-access-token once, and again only as the token nears its expiry. The token
// is held in memory alone, and never printed or written anywhere.
type azureToken struct {
	scope string
	run   azRunner
	now   func() time.Time

	// mu guards the cache, and is held across a refresh, so requests that find the token stale
	// together run az once.
	mu      sync.Mutex
	token   string
	expires time.Time
}

// Token returns the cached token, or a new one when there is none or it expires within
// tokenMargin.
func (a *azureToken) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Before(a.expires.Add(-tokenMargin)) {
		return a.token, nil
	}
	out, err := a.run(ctx, "account", "get-access-token", "--resource", a.scope, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("az account get-access-token: %w", err)
	}
	token, expires, err := parseAzToken(out)
	if err != nil {
		return "", fmt.Errorf("az account get-access-token: %w", err)
	}
	a.token, a.expires = token, expires
	return token, nil
}

// azTokenTime is the layout of az's expiresOn, a time in the local zone.
const azTokenTime = "2006-01-02 15:04:05.999999"

// parseAzToken reads the token and its expiry from az account get-access-token's JSON. The
// expiry is expires_on, in seconds since the epoch, which newer versions of az give as a number
// and older ones as a string. When expires_on is absent, the expiry is expiresOn, in the local
// zone. An error never quotes the output, which holds the token.
func parseAzToken(out []byte) (string, time.Time, error) {
	var v struct {
		AccessToken string          `json:"accessToken"`
		ExpiresOn   string          `json:"expiresOn"`
		ExpiresOnTS json.RawMessage `json:"expires_on"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", time.Time{}, errors.New("the output isn't the JSON of a token")
	}
	if v.AccessToken == "" {
		return "", time.Time{}, errors.New("the output has no accessToken")
	}
	if ts := strings.Trim(string(v.ExpiresOnTS), `"`); ts != "" {
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("expires_on %q isn't seconds since the epoch", ts)
		}
		return v.AccessToken, time.Unix(sec, 0), nil
	}
	if v.ExpiresOn != "" {
		t, err := time.ParseInLocation(azTokenTime, v.ExpiresOn, time.Local)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("expiresOn %q isn't a time", v.ExpiresOn)
		}
		return v.AccessToken, t, nil
	}
	return "", time.Time{}, errors.New("the output has no expiry")
}
