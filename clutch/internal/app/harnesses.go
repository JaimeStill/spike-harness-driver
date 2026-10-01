package app

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// harnessSpec holds a harness's name, the executable it runs, what --provider, --model, and
// --harness-vision-model take when their flags aren't set, how the scenarios talk to it, and
// how its driver is built from the flags.
type harnessSpec struct {
	name, command           string
	provider, model, vision string
	profile                 scenario.Profile
	driver                  func(i *Infrastructure) (harness.Driver, error)
}

// harnesses are the harnesses, in the order help and errors list them. Adding one is an entry
// here, a profile in the scenario package, and the adapter's import. init builds the table,
// since its builders read the flags through the Infrastructure, which looks the table up.
var harnesses []harnessSpec

func init() {
	harnesses = []harnessSpec{
		{
			name:     harnessPi,
			command:  "pi",
			provider: providerLlama,
			model:    "unsloth/gpt-oss-120b-GGUF:Q4_K_M",
			vision:   qwenVision,
			profile:  scenario.Pi,
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
					d.AgentDir = filepath.Join(i.cfg.State, "pi-agent")
					d.Providers = map[string]pi.Provider{providerAzure: {
						BaseURL: os.Getenv("AZURE_OPENAI_BASE_URL"),
						APIKey:  "!az account get-access-token --resource " + i.cfg.AzureScope + " --query accessToken -o tsv",
						Models:  []pi.Model{{ID: i.Options().Model, Image: true, Reasoning: true}},
					}}
				}
				return d, nil
			},
		},
		{
			name:     harnessClaude,
			command:  "claude",
			provider: claude.Provider,
			model:    "haiku",
			vision:   "haiku",
			profile:  scenario.Claude,
			driver: func(i *Infrastructure) (harness.Driver, error) {
				// Claude Code keeps its own sessions. The cache is the one Pi's driver uses: each
				// entry is named by its content, so the two don't meet.
				return claude.Driver{CacheDir: filepath.Join(i.cfg.State, "cache")}, nil
			},
		},
		{
			name:     harnessOpenCode,
			command:  "opencode",
			provider: providerLlama,
			model:    "unsloth/gpt-oss-120b-GGUF:Q4_K_M",
			vision:   qwenVision,
			profile:  scenario.OpenCode,
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

// openCodeProviders are the endpoints an OpenCode session runs on, which the driver writes
// into OpenCode's configuration. The router's are the harness vision model, listed as taking
// images, and the session's model, which the driver lists as text only when it isn't the
// vision model, so OpenCode sees what each model takes.
//
// On the azure provider, OpenCode talks to Azure's v1 API through @ai-sdk/openai: the
// OpenAI-compatible package sends max_tokens, which Azure's reasoning models reject. Its key is
// an Entra ID token, fetched once here: OpenCode's configuration takes no command, so a session
// that outlives the token, about an hour, fails.
func (i *Infrastructure) openCodeProviders() (map[string]opencode.Provider, error) {
	vision := cmp.Or(i.cfg.HarnessVisionModel, i.defaultVision())
	providers := map[string]opencode.Provider{
		providerLlama: {
			// LLAMA_BASE_URL is the router's root; OpenCode's provider takes the
			// OpenAI-compatible base.
			BaseURL: strings.TrimSuffix(os.Getenv("LLAMA_BASE_URL"), "/") + "/v1",
			Models:  map[string]opencode.Model{vision: {Image: true}},
		},
	}
	if i.provider() == providerAzure {
		token, err := i.azureToken().Token(context.Background())
		if err != nil {
			return nil, err
		}
		providers[providerAzure] = opencode.Provider{
			NPM:     "@ai-sdk/openai",
			BaseURL: os.Getenv("AZURE_OPENAI_BASE_URL"),
			APIKey:  token,
			Models:  map[string]opencode.Model{i.Options().Model: {Image: true}},
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
