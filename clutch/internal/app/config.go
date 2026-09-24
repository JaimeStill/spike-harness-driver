package app

import (
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
)

// Config holds the persistent flags, read by the layers once cobra has parsed them.
type Config struct {
	Harness  string
	Provider string
	Model    string
	State    string
	All      bool
	// Skills and Tools are directories of skills and command tools that every session offers.
	Skills []string
	Tools  []string
}

// bind registers cfg's flags on fs.
func (cfg *Config) bind(fs *pflag.FlagSet) {
	fs.StringVar(&cfg.Harness, "harness", "pi", "the harness to drive: pi")
	fs.StringVar(&cfg.Provider, "provider", "llama.cpp", "the harness's model provider")
	fs.StringVar(&cfg.Model, "model", "unsloth/gpt-oss-120b-GGUF:Q4_K_M", "the provider's model ID")
	fs.StringVar(&cfg.State, "state", filepath.Join(os.TempDir(), "clutch"), "where the harness's sessions and the exchange records are kept; the default is under the\n"+
		"temporary directory, so it doesn't survive a reboot")
	fs.BoolVar(&cfg.All, "all", false, "also print harness events with no normalized meaning")
	fs.StringArrayVar(&cfg.Skills, "skills", nil, "a directory of skills, one per subdirectory, that every session offers; repeatable")
	fs.StringArrayVar(&cfg.Tools, "tools", nil, "a directory of command tools, one per subdirectory, that every session offers; repeatable")
}
