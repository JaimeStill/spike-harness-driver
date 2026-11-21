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
	"regexp"
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

const modelTimeout = 10 * time.Minute

// Infrastructure resolves the harness the flags name, the store that keeps exchange records,
// both under the --state directory, the skills and command tools the --skills and --tools
// directories hold, and the direct model clients for the --target endpoint. It opens nothing
// itself: each session the domain opens starts, and ends, its own harness process, and each
// model request makes its own connection.
type Infrastructure struct {
	cfg    *Config
	tools  []harness.Tool
	skills []harness.Skill
	http   *http.Client
	az     azRunner

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
	if i.provider() == providerAzure && !scopeForm.MatchString(i.cfg.AzureScope) {
		return fmt.Errorf("--azure-scope %q: want an https URL of letters, digits, and . - / : only", i.cfg.AzureScope)
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

var scopeForm = regexp.MustCompile(`^https://[A-Za-z0-9./:-]+$`)

func loadSkills(dirs []string) ([]harness.Skill, error) {
	var all []harness.Skill
	where := map[string]string{}
	for _, dir := range dirs {
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
	return h.driver(i)
}

func (i *Infrastructure) spec() harnessSpec {
	h, _ := lookupHarness(i.cfg.Harness)
	return h
}

func (i *Infrastructure) provider() string { return cmp.Or(i.cfg.Provider, i.spec().provider) }

func (i *Infrastructure) defaultModel() string {
	if i.provider() == providerAzure {
		return azureModel
	}
	return i.spec().model
}

func (i *Infrastructure) defaultVision() string {
	if i.provider() == providerAzure {
		return azureModel
	}
	return i.spec().vision
}

func (i *Infrastructure) harnessVision() string {
	return cmp.Or(i.cfg.HarnessVisionModel, i.defaultVision())
}

func (i *Infrastructure) defaultTakesImages() bool { return i.provider() == providerAzure }

// Profile returns how the scenarios talk to the harness the flags name.
func (i *Infrastructure) Profile() scenario.Profile { return i.spec().profile }

// Options returns the session options the flags name, with the store that keeps exchange
// records and the skills and tools Validate loaded.
func (i *Infrastructure) Options() harness.Options {
	return harness.Options{
		Provider: i.provider(),
		Model:    cmp.Or(i.cfg.Model, i.defaultModel()),
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
	harnessAzure, targetAzureNeeds := i.azureNeeds(true, false), i.azureNeeds(false, true)
	return scenario.Needs{
		Harness: append([]scenario.Need{executable, i.llamaNeed(true, false)}, harnessAzure...),
		Models:  append([]scenario.Need{i.llamaNeed(false, true)}, targetAzureNeeds...),
		Both:    append([]scenario.Need{executable, i.llamaNeed(true, true)}, i.azureNeeds(true, true)...),
	}
}

func (i *Infrastructure) azureNeeds(provider, target bool) []scenario.Need {
	var when []string
	if provider {
		when = append(when, "the provider")
	}
	if target {
		when = append(when, "the target")
	}
	used := func() bool {
		return (provider && i.provider() == providerAzure) || (target && i.cfg.Target == targetAzure)
	}
	return []scenario.Need{
		{
			What: "AZURE_OPENAI_BASE_URL set when " + strings.Join(when, " or ") + " is azure",
			Check: func(context.Context) error {
				if used() && os.Getenv("AZURE_OPENAI_BASE_URL") == "" {
					return errors.New("AZURE_OPENAI_BASE_URL is not set")
				}
				return nil
			},
		},
		{
			What: "the Azure CLI, az, on the PATH when " + strings.Join(when, " or ") + " is azure",
			Check: func(context.Context) error {
				if !used() {
					return nil
				}
				_, err := exec.LookPath("az")
				return err
			},
		},
	}
}

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

const (
	targetLlama = "llama.cpp"
	targetAzure = "azure"
)

const providerLlama = "llama.cpp"

const (
	providerAzure = "azure"
	azureModel    = "gpt-5-mini"
)

const routerVision = "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0"

type target struct {
	name                 string
	vision, embed, audio string
	audioInChat          bool
}

var targets = []target{
	{
		name:        targetLlama,
		vision:      routerVision,
		embed:       "ggml-org/embeddinggemma-2-GGUF:Q8_0",
		audio:       "ggml-org/gemma-4-E4B-it-GGUF:Q8_0",
		audioInChat: true,
	},
	{
		name:   targetAzure,
		vision: "gpt-5-mini",
		embed:  "text-embedding-3-small",
		audio:  "gpt-4o-mini-transcribe",
	},
}

func targetNames() []string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.name
	}
	return names
}

func lookupTarget(name string) (target, error) {
	for _, t := range targets {
		if t.name == name {
			return t, nil
		}
	}
	return target{}, fmt.Errorf("unknown target %q (known: %s)", name, strings.Join(targetNames(), ", "))
}

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
		Target:             i.cfg.Target,
		VisionModel:        cmp.Or(i.cfg.VisionModel, t.vision),
		EmbedModel:         cmp.Or(i.cfg.EmbedModel, t.embed),
		AudioModel:         cmp.Or(i.cfg.AudioModel, t.audio),
		AudioInChat:        t.audioInChat,
		HarnessVision:      i.harnessVision(),
		DefaultTakesImages: i.defaultTakesImages(),
	}
	switch t.name {
	case targetLlama:
		base := os.Getenv("LLAMA_BASE_URL")
		if base == "" {
			return scenario.Models{}, errors.New("LLAMA_BASE_URL is not set")
		}
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

func azureAudioBase(base, deployment string) (string, error) {
	root, ok := strings.CutSuffix(strings.TrimSuffix(base, "/"), "/v1")
	if !ok {
		return "", fmt.Errorf("AZURE_OPENAI_BASE_URL %q doesn't end in /v1", base)
	}
	return root + "/deployments/" + url.PathEscape(deployment), nil
}

func (i *Infrastructure) azureToken() *azureToken {
	i.tokenOnce.Do(func() {
		i.token = &azureToken{scope: i.cfg.AzureScope, run: i.az, now: time.Now}
	})
	return i.token
}

type azRunner func(ctx context.Context, args ...string) ([]byte, error)

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

const tokenMargin = 5 * time.Minute

type azureToken struct {
	scope string
	run   azRunner
	now   func() time.Time

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

const azTokenTime = "2006-01-02 15:04:05.999999"

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
