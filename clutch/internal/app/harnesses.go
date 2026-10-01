package app

import (
	"cmp"
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
	driver                  func(i *Infrastructure) harness.Driver
}

// harnesses are the harnesses, in the order help and errors list them. Adding one is an entry
// here, a profile in the scenario package, and the adapter's import.
var harnesses = []harnessSpec{
	{
		name:     harnessPi,
		command:  "pi",
		provider: providerLlama,
		model:    "unsloth/gpt-oss-120b-GGUF:Q4_K_M",
		vision:   qwenVision,
		profile:  scenario.Pi,
		driver: func(i *Infrastructure) harness.Driver {
			return pi.Driver{
				SessionDir: filepath.Join(i.cfg.State, "pi"),
				// Beside Pi's sessions, so the files a session's history names last as long.
				CacheDir: filepath.Join(i.cfg.State, "cache"),
			}
		},
	},
	{
		name:     harnessClaude,
		command:  "claude",
		provider: claude.Provider,
		model:    "haiku",
		vision:   "haiku",
		profile:  scenario.Claude,
		driver: func(i *Infrastructure) harness.Driver {
			// Claude Code keeps its own sessions. The cache is the one Pi's driver uses: each
			// entry is named by its content, so the two don't meet.
			return claude.Driver{CacheDir: filepath.Join(i.cfg.State, "cache")}
		},
	},
	{
		name:     harnessOpenCode,
		command:  "opencode",
		provider: providerLlama,
		model:    "unsloth/gpt-oss-120b-GGUF:Q4_K_M",
		vision:   qwenVision,
		profile:  scenario.OpenCode,
		driver: func(i *Infrastructure) harness.Driver {
			return opencode.Driver{
				// OpenCode's configuration, data, and sessions, in place of the user's.
				StateDir:  filepath.Join(i.cfg.State, "opencode"),
				CacheDir:  filepath.Join(i.cfg.State, "cache"),
				Providers: i.openCodeProviders(),
			}
		},
	},
}

// openCodeProviders are the endpoints an OpenCode session runs on, which the driver writes
// into OpenCode's configuration. The router's are the harness vision model, listed as taking
// images, and the session's model, which the driver lists as text only when it isn't the
// vision model, so OpenCode sees what each model takes.
func (i *Infrastructure) openCodeProviders() map[string]opencode.Provider {
	// OpenCode's own default, qwenVision, written out: reading it from the table would make the
	// table refer to itself.
	vision := cmp.Or(i.cfg.HarnessVisionModel, qwenVision)
	return map[string]opencode.Provider{
		providerLlama: {
			// LLAMA_BASE_URL is the router's root; OpenCode's provider takes the
			// OpenAI-compatible base.
			BaseURL: strings.TrimSuffix(os.Getenv("LLAMA_BASE_URL"), "/") + "/v1",
			Models:  map[string]opencode.Model{vision: {Image: true}},
		},
	}
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
