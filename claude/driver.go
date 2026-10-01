package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/cache"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
	"github.com/JaimeStill/spike-harness-driver/mcpbridge"
)

// streamArgs run Claude Code headless over stream-json in both directions, with the partial
// messages that carry text as it streams.
var streamArgs = []string{
	"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
	"--include-partial-messages",
}

// isolationArgs keep the user's settings, CLAUDE.md files, hooks, and MCP servers out of the
// session, so the prompt and the tools are the driver's: no setting sources load, and only the
// MCP servers --mcp-config names connect. Permission prompts come to the driver as
// can_use_tool requests. --bare would isolate more, but it refuses the subscription login.
var isolationArgs = []string{"--setting-sources", "", "--strict-mcp-config", "--permission-prompts", "host"}

// isolationEnv keeps auto memory out of the session and the CLI from updating itself under the
// driver. CLAUDECODE is set when the driver runs inside a Claude Code session, and would
// change how the harness behaves.
var isolationEnv = []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "DISABLE_AUTOUPDATER=1", "CLAUDECODE="}

// pluginName is the name of the plugin that carries the session's skills. Claude Code names
// each skill <plugin>:<skill>.
const pluginName = "driver"

// Provider is the one model provider Claude Code runs on in this driver: Anthropic's, through
// the user's own sign-in. Claude Code runs Claude models only.
const Provider = "anthropic"

// Driver starts one Claude Code process per session. Claude Code persists each session, and a
// later process resumes it by ID from any working directory.
type Driver struct {
	// Command is the Claude Code executable. Empty means "claude" on the PATH.
	Command string
	// ConfigDir is Claude Code's configuration directory, where it keeps its sessions. Empty
	// means $CLAUDE_CONFIG_DIR, or ~/.claude.
	ConfigDir string
	// CacheDir is where the driver keeps the plugin that carries the session's skills, under a
	// name its content decides, so a resumed session finds the skills its history names. It
	// must belong to the current user and be writable by no one else. Empty writes the plugin
	// into each session's temporary directory, which Close removes.
	CacheDir string
	// Env adds variables to Claude Code's environment.
	Env []string
	// WaitDelay is how long Claude Code has to exit after Close before it is killed. Zero
	// means five seconds.
	WaitDelay time.Duration
	// ListWait is how long a prompt waits for Claude Code to list the tools again after the
	// respond tool changed. Zero means five seconds.
	ListWait time.Duration
}

var _ harness.Driver = Driver{}

// Open starts Claude Code on the session opts.SessionID names, resuming it when Claude Code
// holds it and creating it otherwise, or on a new session under a new ID when it names none.
// opts.Tools reach the model through the driver's MCP server, opts.Skills through a plugin, and
// opts.HarnessTools, when set, is Claude Code's list of built-in tools. ctx bounds the start-up
// handshake only; the session lives until Close.
func (d Driver) Open(ctx context.Context, opts harness.Options) (*harness.Session, error) {
	if opts.Provider != "" && opts.Provider != Provider {
		return nil, fmt.Errorf("claude: provider %q: Claude Code runs on %q only", opts.Provider, Provider)
	}
	id, resume, err := d.sessionID(opts.SessionID)
	if err != nil {
		return nil, err
	}
	if err := lost(ctx, opts, id, resume); err != nil {
		return nil, err
	}
	tools, err := mcpbridge.New(opts.Tools, mcpbridge.Options{Name: serverName})
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	tmp, err := os.MkdirTemp("", "claude-driver-")
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	remove := func() error { return os.RemoveAll(tmp) }
	plugin, err := writePlugin(opts.Skills, cmp.Or(d.CacheDir, tmp), tmp)
	if err != nil {
		_ = remove()
		return nil, err
	}

	args := slices.Concat(streamArgs, isolationArgs)
	mcpConfig, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{serverName: map[string]string{"type": "sdk", "name": serverName}},
	})
	args = append(args, "--mcp-config", string(mcpConfig))
	allowed := []string{"mcp__" + serverName}
	if opts.HarnessTools != nil {
		args = append(args, "--tools", strings.Join(opts.HarnessTools, ","))
		allowed = append(allowed, opts.HarnessTools...)
	}
	args = append(args, "--allowedTools", strings.Join(allowed, ","))
	if plugin != "" {
		args = append(args, "--plugin-dir", plugin)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if resume {
		args = append(args, "--resume", id)
	} else {
		args = append(args, "--session-id", id)
	}

	conn := newConnection(tools, opts.HarnessTools, cmp.Or(d.ListWait, 5*time.Second))
	conn.remove = remove
	conn.tunnel, err = tools.Tunnel(context.Background(), mcpbridge.WithOutbound(conn.outbound))
	if err != nil {
		_ = remove()
		return nil, fmt.Errorf("claude: %w", err)
	}
	p, err := stdio.Start(stdio.Spec{
		Name: cmp.Or(d.Command, "claude"), Args: args, Dir: opts.Dir,
		Env: slices.Concat(isolationEnv, d.Env), WaitDelay: d.WaitDelay,
	})
	if err != nil {
		_ = conn.tunnel.Close()
		_ = remove()
		return nil, err
	}
	conn.client = stdio.NewClient(p, newCodec(), conn.answer)
	if _, err := conn.client.Call(ctx, controlRequest{Subtype: "initialize"}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	s, err := harness.NewSession(ctx, id, conn, opts.Store)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

// sessionID returns the session's ID and whether Claude Code already holds it. A new session's
// ID is a new UUID, and an ID the caller names must be one, as Claude Code requires.
func (d Driver) sessionID(id string) (string, bool, error) {
	if id == "" {
		return uuid.New().String(), false, nil
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return "", false, fmt.Errorf("claude: session ID %q: %w", id, err)
	}
	id = u.String()
	held, err := d.holds(id)
	return id, held, err
}

// holds reports whether Claude Code keeps a transcript of session id, in any project: Claude
// Code keeps a session's transcript under a directory named after its working directory, and
// resumes it from any.
func (d Driver) holds(id string) (bool, error) {
	dir := d.ConfigDir
	if dir == "" {
		dir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("claude: find the config directory: %w", err)
		}
		dir = filepath.Join(home, ".claude")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "projects", "*", id+".jsonl"))
	return len(matches) > 0, err
}

// lost fails with harness.ErrJournalMismatch when the caller names a session Claude Code
// keeps no transcript of, but the store holds exchange records for: Claude Code lost the
// session, and opening it would start a fresh one under the same ID, silently. It stands in
// for the journal check a harness.Journal gives, which Claude Code's stream doesn't offer.
func lost(ctx context.Context, opts harness.Options, id string, resume bool) error {
	if resume || opts.SessionID == "" || opts.Store == nil {
		return nil
	}
	recs, err := opts.Store.Records(ctx, id)
	if err != nil {
		return fmt.Errorf("claude: records of session %s: %w", id, err)
	}
	if len(recs) > 0 {
		return fmt.Errorf("%w: session %s has %d recorded exchanges, and Claude Code keeps no transcript of it",
			harness.ErrJournalMismatch, id, len(recs))
	}
	return nil
}

// writePlugin writes a plugin that carries skills into root, and returns its directory, or ""
// for no skills. It stages the plugin in tmp, then caches it under a name its content decides,
// so a resumed session finds the files its history names.
func writePlugin(skills []harness.Skill, root, tmp string) (string, error) {
	if len(skills) == 0 {
		return "", nil
	}
	staged := filepath.Join(tmp, "plugin")
	manifest := []byte(`{"name":"` + pluginName + `","version":"0.0.0","description":"The skills a driver loads into a session."}` + "\n")
	if err := os.MkdirAll(filepath.Join(staged, ".claude-plugin"), 0o700); err != nil {
		return "", fmt.Errorf("claude: plugin: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staged, ".claude-plugin", "plugin.json"), manifest, 0o600); err != nil {
		return "", fmt.Errorf("claude: plugin: %w", err)
	}
	for _, s := range skills {
		if s.Name == "" || strings.ContainsAny(s.Name, `/\`) || s.Name == "." || s.Name == ".." {
			return "", fmt.Errorf("claude: skill %q: not a directory name", s.Name)
		}
		fsys := s.FS
		if fsys == nil {
			fsys = os.DirFS(s.Dir)
		}
		if err := os.CopyFS(filepath.Join(staged, "skills", s.Name), fsys); err != nil {
			return "", fmt.Errorf("claude: skill %s: %w", s.Name, err)
		}
	}
	dir, err := cache.FS(filepath.Join(root, "plugins"), pluginName, os.DirFS(staged))
	if err != nil {
		return "", fmt.Errorf("claude: plugin: %w", err)
	}
	return dir, nil
}
