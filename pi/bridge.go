package pi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// bridgeSource is the bridge extension, which the driver writes out and loads into every Pi
// it starts. It is the driver's required baseline: discovery stays off, so Pi runs this one
// extension and none of the user's.
//
//go:embed extension/bridge.ts
var bridgeSource []byte

// The titles of the bridge's dialogs, and the tool it registers for a structured response.
const (
	callTitle     = "pi-driver:call"
	exchangeTitle = "pi-driver:exchange"
	respondTool   = "respond"
)

// toolsEnv names the variable that points the bridge at the session's tool spec file.
const toolsEnv = "PI_DRIVER_TOOLS"

// toolSpec is a tool as the bridge registers it.
type toolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// call is the body of a pi-driver:call dialog.
type call struct {
	Tool   string          `json:"tool"`
	CallID string          `json:"callId"`
	Args   json.RawMessage `json:"args"`
}

// answer is the driver's answer to a bridge dialog.
type answer struct {
	Result *string          `json:"result,omitempty"`
	Error  *string          `json:"error,omitempty"`
	Schema *json.RawMessage `json:"schema,omitempty"`
}

// emptySchema is the arguments schema of a tool that declares none.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// bridge is what one session's Pi loads beyond itself: the bridge extension, the spec of the
// session's tools, and its skills, all in a directory of the session's own that Close
// removes.
type bridge struct {
	dir    string
	tools  map[string]harness.Tool
	args   []string
	env    []string
	remove func() error
}

// newBridge writes the bridge, the tool spec, and the skills under a new temporary directory,
// and returns the arguments and environment that load them into Pi. Tool names must be
// unique and must not be respond, which the bridge keeps for structured responses.
func newBridge(opts harness.Options) (*bridge, error) {
	tools := map[string]harness.Tool{}
	specs := []toolSpec{}
	for _, t := range opts.Tools {
		switch {
		case t.Name == respondTool:
			return nil, fmt.Errorf("pi: tool %q: the name is the bridge's own", t.Name)
		case t.Handler == nil:
			return nil, fmt.Errorf("pi: tool %q has no handler", t.Name)
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
	b := &bridge{dir: dir, tools: tools, remove: func() error { return os.RemoveAll(dir) }}
	if err := b.write(opts, specs); err != nil {
		_ = b.remove()
		return nil, fmt.Errorf("pi: bridge: %w", err)
	}
	return b, nil
}

// write writes the bridge's files and builds the arguments that load them.
func (b *bridge) write(opts harness.Options, specs []toolSpec) error {
	ext := filepath.Join(b.dir, "bridge.ts")
	if err := os.WriteFile(ext, bridgeSource, 0o600); err != nil {
		return err
	}
	spec, err := json.Marshal(specs)
	if err != nil {
		return err
	}
	specFile := filepath.Join(b.dir, "tools.json")
	if err := os.WriteFile(specFile, spec, 0o600); err != nil {
		return err
	}
	b.args = []string{"-e", ext}
	b.env = []string{toolsEnv + "=" + specFile}

	// Pi reads skills from disk, so each one's FS is written out under its name.
	for _, s := range opts.Skills {
		if s.Name == "" || strings.ContainsAny(s.Name, `/\`) || s.Name == "." || s.Name == ".." {
			return fmt.Errorf("skill %q: not a directory name", s.Name)
		}
		skillDir := filepath.Join(b.dir, "skills", s.Name)
		if err := os.CopyFS(skillDir, s.FS); err != nil {
			return fmt.Errorf("skill %s: %w", s.Name, err)
		}
		b.args = append(b.args, "--skill", skillDir)
	}

	// Pi never registers a tool its allowlist leaves out, so the allowlist names the
	// session's tools and respond as well as the harness's own.
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

// answerCall runs the tool body names and renders the answer. A tool error, an unknown tool, or
// a malformed call is the model's to see, as a failed tool result.
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

// answerExchange renders the schema of the exchange whose run is starting, or none.
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
	b, _ := json.Marshal(a)
	return string(b)
}
