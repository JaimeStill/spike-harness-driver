package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
)

// Config holds the persistent flags, read by the layers once cobra has parsed them.
type Config struct {
	Harness string
	// Provider and Model are the harness's model provider and model ID. Empty takes the
	// harness's own, which is why the flags' defaults are empty: the default depends on
	// --harness.
	Provider string
	Model    string
	State    string
	All      bool
	// Skills and Tools are directories of skills and command tools that every session offers.
	Skills []string
	Tools  []string
	// Target names the endpoint the direct model client talks to.
	Target string
	// VisionModel, EmbedModel, and AudioModel are the direct client's model IDs. Empty takes
	// the target's default.
	VisionModel string
	EmbedModel  string
	AudioModel  string
	// HarnessVisionModel is the provider's model a harness session runs on when it is sent an
	// image. Empty takes the harness's own.
	HarnessVisionModel string
	// AzureScope is the Entra ID scope of the token the azure target authenticates with.
	AzureScope string
}

// bind registers cfg's flags on fs.
func (cfg *Config) bind(fs *pflag.FlagSet) {
	fs.StringVar(&cfg.Harness, "harness", harnessPi, "the harness to drive: "+strings.Join(harnessNames(), " | "))
	fs.StringVar(&cfg.Provider, "provider", "", "the harness's model provider; empty takes the harness's own: "+defaultsHelp(func(h harnessSpec) string { return h.provider }))
	fs.StringVar(&cfg.Model, "model", "", "the provider's model ID; empty takes the harness's own: "+defaultsHelp(func(h harnessSpec) string { return h.model }))
	fs.StringVar(&cfg.State, "state", defaultState(), "where the harness's sessions, the exchange records, and the files the driver loads into\n"+
		"the harness are kept; it must be yours alone, since the harness runs code from it")
	fs.BoolVar(&cfg.All, "all", false, "also print harness events with no normalized meaning")
	fs.StringArrayVar(&cfg.Skills, "skills", nil, "a directory of skills, one per subdirectory, that every session offers; repeatable")
	fs.StringArrayVar(&cfg.Tools, "tools", nil, "a directory of command tools, one per subdirectory, that every session offers; repeatable")
	fs.StringVar(&cfg.Target, "target", targetLlama, "the endpoint the direct model client talks to: "+strings.Join(targetNames(), " | "))
	fs.StringVar(&cfg.VisionModel, "vision-model", "", "the direct client's model ID for images; empty takes the target's default")
	fs.StringVar(&cfg.EmbedModel, "embed-model", "", "the direct client's model ID for embeddings; empty takes the target's default")
	fs.StringVar(&cfg.AudioModel, "audio-model", "", "the direct client's model ID for audio; empty takes the target's default")
	fs.StringVar(&cfg.HarnessVisionModel, "harness-vision-model", "",
		"the provider's model ID for a harness session sent an image, which keeps --provider whatever --target is; empty takes the harness's own: "+
			defaultsHelp(func(h harnessSpec) string { return h.vision }))
	fs.StringVar(&cfg.AzureScope, "azure-scope", "https://ai.azure.com", "the Entra ID scope of the azure target's token")
}

// defaultsHelp lists what each harness takes for a flag left empty, for the flag's help.
func defaultsHelp(of func(harnessSpec) string) string {
	parts := make([]string, len(harnesses))
	for i, h := range harnesses {
		parts[i] = h.name + " " + of(h)
	}
	return strings.Join(parts, ", ")
}

// defaultState is clutch's directory in the user's cache directory, which, unlike the shared
// temporary directory, no other user can write to. It falls back to the temporary directory
// when the user has no cache directory.
func defaultState() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "clutch")
	}
	return filepath.Join(os.TempDir(), "clutch")
}
