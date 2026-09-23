package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// rpcArgs start Pi in RPC mode with an in-memory session, and without the user's extensions,
// skills, and context files, which would otherwise change the prompt or send extension UI
// requests.
var rpcArgs = []string{"--mode", "rpc", "--no-session", "-ne", "-ns", "-nc"}

// Driver starts one Pi process per session.
type Driver struct {
	// Command is the Pi executable. Empty means "pi" on the PATH.
	Command string
	// Env adds variables to Pi's environment, such as LLAMA_BASE_URL.
	Env []string
	// CloseTimeout bounds how long Close waits for Pi to exit before killing it. Zero means
	// five seconds.
	CloseTimeout time.Duration
}

var _ harness.Driver = Driver{}

// Open starts Pi, selects the model when opts names one, and takes Pi's session ID. ctx bounds
// the start-up handshake only; the session lives until Close.
//
// The model is set over RPC rather than with --model, which would read a model ID's
// ":Q4_K_M" suffix as a thinking level.
func (d Driver) Open(ctx context.Context, opts harness.Options) (*harness.Session, error) {
	name := d.Command
	if name == "" {
		name = "pi"
	}
	p, err := stdio.Start(stdio.Spec{
		Name: name, Args: rpcArgs, Dir: opts.Dir, Env: d.Env, CloseTimeout: d.CloseTimeout,
	})
	if err != nil {
		return nil, err
	}
	c := stdio.NewClient(p, codec{})
	id, err := handshake(ctx, c, opts)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return harness.NewSession(id, connection{c}), nil
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
