package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/pi"
)

// Infrastructure resolves the harness the flags name, and the store that keeps exchange
// records, both under the --state directory. It opens nothing itself: each session the domain
// opens starts, and ends, its own harness process.
type Infrastructure struct {
	cfg *Config
}

func newInfrastructure(cfg *Config) *Infrastructure {
	return &Infrastructure{cfg: cfg}
}

// Validate fails when the flags name a harness clutch has no adapter for.
func (i *Infrastructure) Validate() error {
	_, err := i.Driver()
	return err
}

// Driver returns the adapter for the harness the flags name.
func (i *Infrastructure) Driver() (harness.Driver, error) {
	switch i.cfg.Harness {
	case "pi":
		return pi.Driver{SessionDir: filepath.Join(i.cfg.State, "pi")}, nil
	default:
		return nil, fmt.Errorf("unknown harness %q (known: pi)", i.cfg.Harness)
	}
}

// Options returns the session options the flags name, with the store that keeps exchange
// records.
func (i *Infrastructure) Options() harness.Options {
	return harness.Options{Provider: i.cfg.Provider, Model: i.cfg.Model, Store: i.Store()}
}

// Store returns the store that keeps exchange records, in the --state directory.
func (i *Infrastructure) Store() harness.Store {
	return filestore.New(filepath.Join(i.cfg.State, "exchanges"))
}

// Needs returns what the harness requires to run, which each scenario checks first.
func (i *Infrastructure) Needs() []scenario.Need {
	return []scenario.Need{
		{
			What: "the harness executable on the PATH",
			Check: func(context.Context) error {
				_, err := exec.LookPath(i.cfg.Harness)
				return err
			},
		},
		{
			What: "LLAMA_BASE_URL set when the provider is llama.cpp",
			Check: func(context.Context) error {
				if i.cfg.Provider == "llama.cpp" && os.Getenv("LLAMA_BASE_URL") == "" {
					return errors.New("LLAMA_BASE_URL is not set")
				}
				return nil
			},
		},
	}
}
