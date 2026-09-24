package pi

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
// session's tools, and its skills. The tool spec is the session's own, in a temporary
// directory that Close removes. The extension and the skills are written once into the
// driver's cache, when it has one, under names their content decides, so every session loads
// them from the same paths, and a resumed session's history still names files that exist.
type bridge struct {
	tools  map[string]harness.Tool
	args   []string
	env    []string
	remove func() error
}

// newBridge writes the bridge's files and returns the arguments and environment that load them
// into Pi. cache is the driver's cache directory; empty keeps everything in the session's
// temporary directory. Tool names must be unique and must not be respond, which the bridge
// keeps for structured responses.
func newBridge(opts harness.Options, cache string) (*bridge, error) {
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
	if cache == "" {
		cache = dir
	}
	b := &bridge{tools: tools, remove: func() error { return os.RemoveAll(dir) }}
	if err := b.write(opts, specs, dir, cache); err != nil {
		_ = b.remove()
		return nil, fmt.Errorf("pi: bridge: %w", err)
	}
	return b, nil
}

// write writes the tool spec to dir and the extension and skills to cache, and builds the
// arguments that load them.
func (b *bridge) write(opts harness.Options, specs []toolSpec, dir, cache string) error {
	ext, err := cached(cache, "bridge", digest(bridgeSource), func(d string) error {
		return os.WriteFile(filepath.Join(d, "bridge.ts"), bridgeSource, 0o600)
	})
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

	// Pi reads skills from disk. A skill already there is loaded where it lives; any other is
	// written into the cache under its name.
	for _, s := range opts.Skills {
		if s.Name == "" || strings.ContainsAny(s.Name, `/\`) || s.Name == "." || s.Name == ".." {
			return fmt.Errorf("skill %q: not a directory name", s.Name)
		}
		skillDir := s.Dir
		if skillDir == "" {
			if skillDir, err = cachedFS(filepath.Join(cache, "skills"), s.Name, s.FS); err != nil {
				return fmt.Errorf("skill %s: %w", s.Name, err)
			}
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

// cachedFS returns the directory under root that holds fsys's files, named by name and a
// digest of every file's path and content.
func cachedFS(root, name string, fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		// Length-prefixed, so no two trees hash alike by moving bytes between files.
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return cached(root, name, hex.EncodeToString(h.Sum(nil))[:12], func(d string) error {
		return os.CopyFS(d, fsys)
	})
}

// digest is the first 12 hex digits of data's SHA-256.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:6])
}

// cached returns the directory root/name-digest, which write fills the first time. It writes
// to a temporary name it then renames, so a concurrent writer of the same content never sees
// the directory half written. The same content always gets the same directory, and changed
// content a new one, so no session's files change under it.
func cached(root, name, digest string, write func(dir string) error) (string, error) {
	dir := filepath.Join(root, name+"-"+digest)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".write-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	staged := filepath.Join(tmp, "d")
	if err := os.Mkdir(staged, 0o755); err != nil {
		return "", err
	}
	if err := write(staged); err != nil {
		return "", err
	}
	// Losing the race to another writer of the same content leaves the directory it wrote.
	if err := os.Rename(staged, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", err
		}
	}
	return dir, nil
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
