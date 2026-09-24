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
	"github.com/JaimeStill/spike-harness-driver/harness/catalog"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/pi"
)

// Infrastructure resolves the harness the flags name, the store that keeps exchange records,
// both under the --state directory, and the skills and command tools the --skills and --tools
// directories hold. It opens nothing itself: each session the domain opens starts, and ends,
// its own harness process.
type Infrastructure struct {
	cfg *Config
	// tools and skills are what Validate loaded from the --tools and --skills directories.
	tools  []harness.Tool
	skills []harness.Skill
}

func newInfrastructure(cfg *Config) *Infrastructure {
	return &Infrastructure{cfg: cfg}
}

// Validate fails when the flags name a harness clutch has no adapter for, or a --skills or
// --tools directory that doesn't load. It keeps what the directories hold for Options, so a
// bad directory fails the command before any harness starts rather than when a session opens.
func (i *Infrastructure) Validate() error {
	if _, err := i.Driver(); err != nil {
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
	switch i.cfg.Harness {
	case "pi":
		return pi.Driver{
			SessionDir: filepath.Join(i.cfg.State, "pi"),
			// Beside Pi's sessions, so the files a session's history names last as long.
			CacheDir: filepath.Join(i.cfg.State, "cache"),
		}, nil
	default:
		return nil, fmt.Errorf("unknown harness %q (known: pi)", i.cfg.Harness)
	}
}

// Options returns the session options the flags name, with the store that keeps exchange
// records and the skills and tools Validate loaded.
func (i *Infrastructure) Options() harness.Options {
	return harness.Options{
		Provider: i.cfg.Provider,
		Model:    i.cfg.Model,
		Store:    i.Store(),
		Tools:    i.tools,
		Skills:   i.skills,
	}
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
