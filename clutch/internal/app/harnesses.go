package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/claude"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/pi"
)

// The harnesses clutch has an adapter for.
const (
	harnessPi     = "pi"
	harnessClaude = "claude"
)

// harnessSpec holds a harness's name, the executable it runs, what --provider, --model, and
// --harness-vision-model take when their flags aren't set, how the scenarios talk to it, and
// how its driver is built from the --state directory.
type harnessSpec struct {
	name, command           string
	provider, model, vision string
	profile                 scenario.Profile
	driver                  func(state string) harness.Driver
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
		driver: func(state string) harness.Driver {
			return pi.Driver{
				SessionDir: filepath.Join(state, "pi"),
				// Beside Pi's sessions, so the files a session's history names last as long.
				CacheDir: filepath.Join(state, "cache"),
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
		driver: func(state string) harness.Driver {
			// Claude Code keeps its own sessions. The cache is the one Pi's driver uses: each
			// entry is named by its content, so the two don't meet.
			return claude.Driver{CacheDir: filepath.Join(state, "cache")}
		},
	},
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
