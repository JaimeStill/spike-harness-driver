package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// rpcArgs start Pi in RPC mode without discovering the user's extensions, skills, and context
// files, which would otherwise change the prompt or send dialogs no one answers. Explicit -e
// and --skill paths still load, which is how the bridge and the session's skills arrive. Pi
// persists the session.
var rpcArgs = []string{"--mode", "rpc", "-ne", "-ns", "-nc"}

// Driver starts one Pi process per session. Pi persists each session, so a later process
// resumes it by ID.
//
// Pi scopes a session ID to the working directory: resuming a session from another directory
// finds nothing, and Pi starts a fresh session under the same ID. The session's exchange
// records then name entries Pi doesn't hold, and Open fails with harness.ErrJournalMismatch.
type Driver struct {
	// Command is the Pi executable. Empty means "pi" on the PATH.
	Command string
	// SessionDir is where Pi stores sessions. Empty means Pi's own default.
	SessionDir string
	// Env adds variables to Pi's environment, such as LLAMA_BASE_URL.
	Env []string
	// WaitDelay is how long Pi has to exit after Close before it is killed. Zero means five
	// seconds.
	WaitDelay time.Duration
}

var _ harness.Driver = Driver{}

// Open starts Pi on the session opts.SessionID names, creating it if Pi has none, or on a new
// session when it names none, with the bridge, opts.Tools, and opts.Skills loaded, and
// opts.HarnessTools, when set, as Pi's tool allowlist. It selects the model when opts names
// one, takes Pi's session ID, and binds the session to Pi's entries through the
// harness.Journal the connection keeps.
// ctx bounds the start-up handshake only; the session lives until Close.
//
// The model is set over RPC rather than with --model, which would read a model ID's
// ":Q4_K_M" suffix as a thinking level.
func (d Driver) Open(ctx context.Context, opts harness.Options) (*harness.Session, error) {
	name := d.Command
	if name == "" {
		name = "pi"
	}
	b, err := newBridge(opts)
	if err != nil {
		return nil, err
	}
	args := slices.Concat(rpcArgs, b.args)
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if d.SessionDir != "" {
		args = append(args, "--session-dir", d.SessionDir)
	}
	p, err := stdio.Start(stdio.Spec{
		Name: name, Args: args, Dir: opts.Dir, Env: slices.Concat(d.Env, b.env), WaitDelay: d.WaitDelay,
	})
	if err != nil {
		_ = b.remove()
		return nil, err
	}
	conn := newConnection(b)
	conn.client = stdio.NewClient(p, codec{}, conn.answer)
	id, err := handshake(ctx, conn.client, opts)
	if err != nil {
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

func handshake(ctx context.Context, c *stdio.Client, opts harness.Options) (string, error) {
	if opts.Model != "" {
		cmd := command{Type: "set_model", Provider: opts.Provider, ModelID: opts.Model}
		if _, err := c.Call(ctx, cmd); err != nil {
			return "", err
		}
	}
	r, err := c.Call(ctx, command{Type: "get_state"})
	if err != nil {
		return "", err
	}
	var st state
	if err := json.Unmarshal(r.Data, &st); err != nil {
		return "", fmt.Errorf("pi: get_state: %w", err)
	}
	return st.SessionID, nil
}
