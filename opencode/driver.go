package opencode

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/cache"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// Provider is an OpenAI-compatible endpoint OpenCode runs models on. The driver writes it into
// OpenCode's configuration, with the session's model listed, so the model is selectable at
// once: OpenCode selects only models its configuration or catalog lists.
type Provider struct {
	// BaseURL is the endpoint's OpenAI-compatible base URL, ending in /v1 or its equivalent.
	BaseURL string
	// Headers are sent with every request, such as an Authorization header carrying a token.
	Headers map[string]string
	// Models describes the models the driver knows. A session's model that isn't among them is
	// listed as text only.
	Models map[string]Model
}

// Model describes one of a provider's models to OpenCode.
type Model struct {
	// Image is whether the model takes images. OpenCode sends an image only to a model that
	// does.
	Image bool
	// Context and Output are the model's token limits; zero leaves them to OpenCode.
	Context, Output int
}

// isolationEnv keeps the user's Claude Code files and outside skills out of the session, and
// keeps OpenCode from fetching its model catalog or updating itself under the driver.
var isolationEnv = []string{
	"OPENCODE_DISABLE_CLAUDE_CODE=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1",
	"OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_AUTOUPDATE=1",
}

// Driver starts one OpenCode process per session, in ACP mode (opencode acp --pure, which
// keeps the user's plugins out). OpenCode keeps its sessions in StateDir, and a later process
// loads one by ID from any working directory.
type Driver struct {
	// Command is the OpenCode executable. Empty means "opencode" on the PATH.
	Command string
	// StateDir holds OpenCode's configuration, data, state, and cache directories in place of
	// the user's, which keeps the user's configuration out and the sessions in one place. It
	// must be the same for a session to be loaded again. Empty uses a temporary directory per
	// session, which Close removes, so no session outlives its process.
	StateDir string
	// CacheDir is where the driver keeps the skills that aren't on disk already, under names
	// their content decides. Empty writes them into the session's temporary directory.
	CacheDir string
	// Providers are the endpoints the sessions run on, by provider ID: the Provider of
	// harness.Options names one.
	Providers map[string]Provider
	// Env adds variables to OpenCode's environment.
	Env []string
	// WaitDelay is how long OpenCode has to exit after Close before it is killed. Zero means
	// five seconds.
	WaitDelay time.Duration
	// ListWait is how long a prompt waits for OpenCode to list the tools again after the
	// respond tool changed. Zero means five seconds.
	ListWait time.Duration
}

var _ harness.Driver = Driver{}

// Open starts OpenCode and loads the session opts.SessionID names, or creates a new one under
// an ID OpenCode assigns when it names none. OpenCode can't create a session under an ID the
// caller chooses, so an ID it doesn't hold fails. opts.Tools reach the model through the
// driver's MCP server, opts.Skills through OpenCode's skill paths, and opts.HarnessTools, when
// set, is OpenCode's tool allowlist. ctx bounds the start-up handshake only.
func (d Driver) Open(ctx context.Context, opts harness.Options) (*harness.Session, error) {
	if _, ok := d.Providers[opts.Provider]; !ok {
		return nil, fmt.Errorf("opencode: provider %q: the driver has none by that name (known: %s)",
			opts.Provider, strings.Join(slices.Sorted(maps.Keys(d.Providers)), ", "))
	}
	tmp, err := os.MkdirTemp("", "opencode-driver-")
	if err != nil {
		return nil, fmt.Errorf("opencode: %w", err)
	}
	remove := func() error { return os.RemoveAll(tmp) }
	fail := func(err error) (*harness.Session, error) {
		_ = remove()
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		if dir, err = os.Getwd(); err != nil {
			return fail(fmt.Errorf("opencode: %w", err))
		}
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return fail(fmt.Errorf("opencode: %w", err))
	}
	config, err := d.config(opts, cmp.Or(d.CacheDir, tmp))
	if err != nil {
		return fail(err)
	}
	tools, err := mcpbridge.New(opts.Tools, mcpbridge.Options{Name: serverName})
	if err != nil {
		return fail(fmt.Errorf("opencode: %w", err))
	}
	endpoint, err := tools.Listen(context.Background())
	if err != nil {
		return fail(fmt.Errorf("opencode: %w", err))
	}
	conn := &connection{tools: tools, endpoint: endpoint, remove: remove, listWait: cmp.Or(d.ListWait, 5*time.Second)}
	if opts.HarnessTools != nil {
		conn.allowed = map[string]bool{}
		for _, t := range opts.HarnessTools {
			conn.allowed[t] = true
		}
	}
	state := cmp.Or(d.StateDir, filepath.Join(tmp, "state"))
	env := slices.Concat(isolationEnv, []string{
		"XDG_CONFIG_HOME=" + filepath.Join(state, "config"),
		"XDG_DATA_HOME=" + filepath.Join(state, "data"),
		"XDG_STATE_HOME=" + filepath.Join(state, "state"),
		"XDG_CACHE_HOME=" + filepath.Join(state, "cache"),
		"OPENCODE_CONFIG_CONTENT=" + string(config),
	}, d.Env)
	p, err := stdio.Start(stdio.Spec{
		Name: cmp.Or(d.Command, "opencode"), Args: []string{"acp", "--pure"}, Dir: dir, Env: env, WaitDelay: d.WaitDelay,
	})
	if err != nil {
		_ = endpoint.Close()
		return fail(err)
	}
	conn.client = stdio.NewClient(p, newCodec(), conn.answer)
	if conn.sessionID, err = handshake(ctx, conn, opts, dir); err != nil {
		_ = conn.Close()
		return nil, err
	}
	s, err := harness.NewSession(ctx, conn.sessionID, conn, opts.Store)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// handshake initializes ACP, creates or loads the session with the driver's MCP server, and
// selects the model, returning the session's ID.
func handshake(ctx context.Context, c *connection, opts harness.Options, dir string) (string, error) {
	if _, err := c.client.Call(ctx, call{Method: "initialize", Params: map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
	}}); err != nil {
		return "", err
	}
	servers := []mcpServer{{Type: "http", Name: serverName, URL: c.endpoint.URL,
		Headers: []httpHeader{{Name: "Authorization", Value: "Bearer " + c.endpoint.Token}}}}
	id := opts.SessionID
	if id == "" {
		r, err := c.client.Call(ctx, call{Method: "session/new", Params: map[string]any{"cwd": dir, "mcpServers": servers}})
		if err != nil {
			return "", err
		}
		var created struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(r.Data, &created); err != nil || created.SessionID == "" {
			return "", fmt.Errorf("opencode: session/new answered no session ID: %s", r.Data)
		}
		id = created.SessionID
	} else if _, err := c.client.Call(ctx, call{Method: "session/load", Params: map[string]any{
		"sessionId": id, "cwd": dir, "mcpServers": servers,
	}}); err != nil {
		return "", fmt.Errorf("opencode: load session %s: %w", id, err)
	}
	if opts.Model != "" {
		if _, err := c.client.Call(ctx, call{Method: "session/set_config_option", Params: map[string]any{
			"sessionId": id, "configId": "model", "value": opts.Provider + "/" + opts.Model,
		}}); err != nil {
			return "", err
		}
	}
	return id, nil
}

// config renders OpenCode's configuration for a session: the session's provider alone, with
// its model listed; the session's skills as skill paths; and, when opts.HarnessTools is set,
// a tool allowlist of those tools and the driver's.
func (d Driver) config(opts harness.Options, cacheDir string) ([]byte, error) {
	p := d.Providers[opts.Provider]
	models := map[string]any{}
	known := maps.Clone(p.Models)
	if known == nil {
		known = map[string]Model{}
	}
	if _, ok := known[opts.Model]; opts.Model != "" && !ok {
		known[opts.Model] = Model{}
	}
	for id, m := range known {
		input := []string{"text"}
		if m.Image {
			input = append(input, "image")
		}
		entry := map[string]any{"name": id, "tool_call": true, "attachment": m.Image,
			"modalities": map[string][]string{"input": input, "output": {"text"}}}
		if m.Context > 0 || m.Output > 0 {
			entry["limit"] = map[string]int{"context": m.Context, "output": m.Output}
		}
		models[id] = entry
	}
	options := map[string]any{"baseURL": p.BaseURL}
	if len(p.Headers) > 0 {
		options["headers"] = p.Headers
	}
	cfg := map[string]any{
		"$schema":           "https://opencode.ai/config.json",
		"enabled_providers": []string{opts.Provider},
		"provider": map[string]any{opts.Provider: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": opts.Provider, "options": options, "models": models,
		}},
		"autoupdate": false,
		"share":      "disabled",
	}
	var paths []string
	for _, s := range opts.Skills {
		if s.Name == "" || strings.ContainsAny(s.Name, `/\`) || s.Name == "." || s.Name == ".." {
			return nil, fmt.Errorf("opencode: skill %q: not a directory name", s.Name)
		}
		dir := s.Dir
		if dir == "" {
			var err error
			if dir, err = cache.FS(filepath.Join(cacheDir, "skills"), s.Name, s.FS); err != nil {
				return nil, fmt.Errorf("opencode: skill %s: %w", s.Name, err)
			}
		}
		paths = append(paths, dir)
	}
	if len(paths) > 0 {
		cfg["skills"] = map[string]any{"paths": paths}
	}
	if opts.HarnessTools != nil {
		allow := map[string]bool{"*": false, toolPrefix + "*": true}
		for _, t := range opts.HarnessTools {
			allow[t] = true
		}
		cfg["tools"] = allow
	}
	return json.Marshal(cfg)
}
