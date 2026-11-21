package pi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/cache"
)

//go:embed extension/bridge.ts extension/respond.ts
var bridgeFiles embed.FS

const (
	callTitle     = "pi-driver:call"
	exchangeTitle = "pi-driver:exchange"
	respondTool   = "respond"
)

const toolsEnv = "PI_DRIVER_TOOLS"

type toolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type call struct {
	Tool   string          `json:"tool"`
	CallID string          `json:"callId"`
	Args   json.RawMessage `json:"args"`
}

type answer struct {
	Result *string          `json:"result,omitempty"`
	Error  *string          `json:"error,omitempty"`
	Schema *json.RawMessage `json:"schema,omitempty"`
}

var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

type bridge struct {
	tools  map[string]harness.Tool
	args   []string
	env    []string
	remove func() error
}

func newBridge(opts harness.Options, cacheDir string) (*bridge, error) {
	tools := map[string]harness.Tool{}
	specs := []toolSpec{}
	for _, t := range opts.Tools {
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if t.Name == respondTool {
			return nil, fmt.Errorf("pi: tool %q: the name is the bridge's own", t.Name)
		}
		if _, dup := tools[t.Name]; dup {
			return nil, fmt.Errorf("pi: two tools are named %q", t.Name)
		}
		tools[t.Name] = t
		schema := t.Schema
		if len(schema) == 0 {
			schema = emptySchema
		}
		specs = append(specs, toolSpec{Name: t.Name, Description: t.Description, Schema: schema})
	}

	dir, err := os.MkdirTemp("", "pi-driver-")
	if err != nil {
		return nil, fmt.Errorf("pi: bridge: %w", err)
	}
	if cacheDir == "" {
		cacheDir = dir
	}
	b := &bridge{tools: tools, remove: func() error { return os.RemoveAll(dir) }}
	if err := b.write(opts, specs, dir, cacheDir); err != nil {
		_ = b.remove()
		return nil, fmt.Errorf("pi: bridge: %w", err)
	}
	return b, nil
}

func (b *bridge) write(opts harness.Options, specs []toolSpec, dir, cacheDir string) error {
	files, err := fs.Sub(bridgeFiles, "extension")
	if err != nil {
		return err
	}
	ext, err := cache.FS(cacheDir, "bridge", files)
	if err != nil {
		return err
	}
	spec, err := json.Marshal(specs)
	if err != nil {
		return err
	}
	specFile := filepath.Join(dir, "tools.json")
	if err := os.WriteFile(specFile, spec, 0o600); err != nil {
		return err
	}
	b.args = []string{"-e", filepath.Join(ext, "bridge.ts")}
	b.env = []string{toolsEnv + "=" + specFile}

	for _, s := range opts.Skills {
		if err := s.Validate(); err != nil {
			return err
		}
		skillDir := s.Dir
		if skillDir == "" {
			if skillDir, err = cache.FS(filepath.Join(cacheDir, "skills"), s.Name, s.FS); err != nil {
				return fmt.Errorf("skill %s: %w", s.Name, err)
			}
		}
		b.args = append(b.args, "--skill", skillDir)
	}

	if opts.HarnessTools != nil {
		allow := slices.Clone(opts.HarnessTools)
		for _, s := range specs {
			allow = append(allow, s.Name)
		}
		allow = append(allow, respondTool)
		b.args = append(b.args, "-t", strings.Join(allow, ","))
	}
	return nil
}

func (b *bridge) answerCall(ctx context.Context, body string) string {
	var c call
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return failure(fmt.Errorf("pi: malformed call: %w", err))
	}
	t, ok := b.tools[c.Tool]
	if !ok {
		return failure(fmt.Errorf("pi: no tool %q", c.Tool))
	}
	result, err := t.Handler(ctx, c.Args)
	if err != nil {
		if ctx.Err() != nil {
			err = errors.Join(err, context.Cause(ctx))
		}
		return failure(err)
	}
	return render(answer{Result: &result})
}

func answerExchange(schema json.RawMessage) string {
	if len(schema) == 0 {
		return render(answer{})
	}
	return render(answer{Schema: &schema})
}

func failure(err error) string {
	msg := err.Error()
	return render(answer{Error: &msg})
}

func render(a answer) string {
	b, err := json.Marshal(a)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"error": fmt.Sprintf("pi: render the answer: %v", err)})
	}
	return string(b)
}
