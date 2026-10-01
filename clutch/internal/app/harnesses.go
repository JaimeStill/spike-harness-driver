package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JaimeStill/spike-harness-driver/claude"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/opencode"
	"github.com/JaimeStill/spike-harness-driver/pi"
)

// The harnesses clutch has an adapter for.
const (
	harnessPi       = "pi"
	harnessClaude   = "claude"
	harnessOpenCode = "opencode"
)

// harnessSpec describes one harness: its name, its executable, the version the conformance suite
// pins, the defaults for --provider, --model, and --harness-vision-model when their flags are
// unset, the providers the conformance suite runs it over, how the scenarios talk to it, and how to
// build its driver from the flags.
type harnessSpec struct {
	name, command           string
	pinned                  string
	provider, model, vision string
	providers               []string
	profile                 scenario.Profile
	driver                  func(i *Infrastructure) (harness.Driver, error)
}

// harnesses is the table of supported harnesses, in the order help and errors list them. Adding one
// is an entry here, a profile in the scenario package, and the adapter's import. init builds the
// table, since its builders read the flags through the Infrastructure, which looks the table up.
var harnesses []harnessSpec

func init() {
	harnesses = []harnessSpec{
		{
			name:     harnessPi,
			command:  "pi",
			pinned:   "0.99.2",
			provider: providerLlama,
			model:    "unsloth/gpt-oss-120b-GGUF:Q4_K_M",
			vision:   qwenVision,
			// Pi and OpenCode run over the router or Azure; Claude Code runs on Anthropic's alone.
			providers: []string{providerLlama, providerAzure},
			profile:   scenario.Pi,
			driver: func(i *Infrastructure) (harness.Driver, error) {
				d := pi.Driver{
					SessionDir: filepath.Join(i.cfg.State, "pi"),
					// Beside Pi's sessions, so the files a session's history names last as long.
					CacheDir: filepath.Join(i.cfg.State, "cache"),
				}
				if i.provider() == providerAzure {
					// Pi takes a provider beyond its built-in ones from the models.json in its agent
					// directory, which then stands in for the user's ~/.pi/agent. Its key is the
					// az command, which Pi runs for each request, so the token never goes stale.
					// The models are the session's and, when it differs, the harness vision model,
					// so either is selectable; every model clutch runs on Azure takes images and
					// reasons.
					models := []pi.Model{{ID: i.Options().Model, Image: true, Reasoning: true}}
					if vision := i.harnessVision(); vision != models[0].ID {
						models = append(models, pi.Model{ID: vision, Image: true, Reasoning: true})
					}
					d.AgentDir = filepath.Join(i.cfg.State, "pi-agent")
					d.Providers = map[string]pi.Provider{providerAzure: {
						BaseURL: os.Getenv("AZURE_OPENAI_BASE_URL"),
						APIKey:  "!az account get-access-token --resource " + i.cfg.AzureScope + " --query accessToken -o tsv",
						Models:  models,
					}}
				}
				return d, nil
			},
		},
		{
			name:      harnessClaude,
			command:   "claude",
			pinned:    "2.1.287",
			provider:  claude.Provider,
			model:     "haiku",
			vision:    "haiku",
			providers: []string{claude.Provider},
			profile:   scenario.Claude,
			driver: func(i *Infrastructure) (harness.Driver, error) {
				// Claude Code keeps its own sessions. The cache is the one Pi's driver uses: each
				// entry is named by its content, so the two don't meet.
				return claude.Driver{CacheDir: filepath.Join(i.cfg.State, "cache")}, nil
			},
		},
		{
			name:      harnessOpenCode,
			command:   "opencode",
			pinned:    "1.18.34",
			provider:  providerLlama,
			model:     "unsloth/gpt-oss-120b-GGUF:Q4_K_M",
			vision:    qwenVision,
			providers: []string{providerLlama, providerAzure},
			profile:   scenario.OpenCode,
			driver: func(i *Infrastructure) (harness.Driver, error) {
				providers, err := i.openCodeProviders()
				if err != nil {
					return nil, err
				}
				return opencode.Driver{
					// OpenCode's configuration, data, and sessions, in place of the user's.
					StateDir:  filepath.Join(i.cfg.State, "opencode"),
					CacheDir:  filepath.Join(i.cfg.State, "cache"),
					Providers: providers,
				}, nil
			},
		},
	}
}

// azTokenTimeout bounds fetching the Entra ID token an OpenCode session on Azure starts with.
// The fetch runs az, which reaches Entra ID over the network, before any harness starts and
// under no context of the command's, so an unreachable network would otherwise hang clutch
// with nothing on screen. az answers in seconds when it can answer at all.
const azTokenTimeout = 30 * time.Second

// openCodeProviders are the endpoints an OpenCode session runs on, which the driver writes
// into OpenCode's configuration. The router's are the harness vision model, listed as taking
// images, and the session's model, which the driver lists as text only when it isn't the
// vision model, so OpenCode sees what each model takes.
//
// On the azure provider, OpenCode talks to Azure's v1 API through @ai-sdk/openai: the
// OpenAI-compatible package sends max_tokens, which Azure's reasoning models reject. Its models
// are the session's and the harness vision model, both taking images, as every model clutch
// runs on Azure does. Its key is an Entra ID token, fetched once here: OpenCode's configuration
// takes no command, so a session that outlives the token, about an hour, fails.
func (i *Infrastructure) openCodeProviders() (map[string]opencode.Provider, error) {
	vision := i.harnessVision()
	providers := map[string]opencode.Provider{
		providerLlama: {
			// LLAMA_BASE_URL is the router's root; OpenCode's provider takes the
			// OpenAI-compatible base.
			BaseURL: strings.TrimSuffix(os.Getenv("LLAMA_BASE_URL"), "/") + "/v1",
			Models:  map[string]opencode.Model{vision: {Image: true}},
		},
	}
	if i.provider() == providerAzure {
		ctx, cancel := context.WithTimeout(context.Background(), azTokenTimeout)
		defer cancel()
		token, err := i.azureToken().Token(ctx)
		if err != nil {
			return nil, err
		}
		providers[providerAzure] = opencode.Provider{
			NPM:     "@ai-sdk/openai",
			BaseURL: os.Getenv("AZURE_OPENAI_BASE_URL"),
			APIKey:  token,
			Models:  map[string]opencode.Model{i.Options().Model: {Image: true}, vision: {Image: true}},
		}
	}
	return providers, nil
}

// harnessNames returns the harnesses' names, in the order help and errors list them.
func harnessNames() []string {
	names := make([]string, len(harnesses))
	for i, h := range harnesses {
		names[i] = h.name
	}
	return names
}

// lookupHarness returns the harness named name.
func lookupHarness(name string) (harnessSpec, error) {
	for _, h := range harnesses {
		if h.name == name {
			return h, nil
		}
	}
	return harnessSpec{}, fmt.Errorf("unknown harness %q (known: %s)", name, strings.Join(harnessNames(), ", "))
}
