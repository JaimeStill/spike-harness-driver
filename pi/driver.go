package pi

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/stdio"
)

// rpcArgs start Pi in RPC mode without discovering the user's extensions, skills, and context
// files, which would otherwise change the prompt or send dialogs no one answers. Explicit -e
// and --skill paths still load, which is how the bridge and the session's skills arrive. Pi
// persists the session.
var rpcArgs = []string{"--mode", "rpc", "-ne", "-ns", "-nc"}

// llamaProvider is the name of Pi's llama.cpp provider, which Pi ships as a built-in extension.
// Since Pi 0.99, -ne disables built-in extensions too, so for a session on this provider the
// driver loads the extension by name. The user's extensions and the other built-ins, such as
// MCP, stay out.
const llamaProvider = "llama.cpp"

// Driver starts one Pi process per session. Pi persists each session, so a later process
// resumes it by ID.
//
// Pi scopes a session ID to the working directory: resuming a session from another directory
// finds nothing, and Pi starts a fresh session under the same ID. The session's exchange
// records then name entries Pi doesn't hold, and Open fails with harness.ErrJournalMismatch.
//
// The driver needs Pi 0.99 or newer, which loads a built-in extension by a "builtin:" name.
type Driver struct {
	// Command is the Pi executable. Empty means "pi" on the PATH.
	Command string
	// SessionDir is where Pi stores sessions. Empty means Pi's own default.
	SessionDir string
	// CacheDir is where the driver keeps what it loads into Pi from files: the bridge
	// extension, and the skills that aren't on disk already. They are written once, under names
	// their content decides, so sessions share them and a resumed session finds the files its
	// history names. Pi runs the bridge as code, so the directory must belong to the current
	// user and be writable by no one else; Open fails otherwise. Empty writes them into each
	// session's temporary directory, which Close removes.
	CacheDir string
	// Env adds variables to Pi's environment, such as LLAMA_BASE_URL.
	Env []string
	// WaitDelay is how long Pi has to exit after Close before it is killed. Zero means five
	// seconds.
	WaitDelay time.Duration
	// CatalogWait is how long Open waits for a llama.cpp model Pi doesn't list yet. Pi starts
	// from the model catalog it saved last and refreshes each provider's catalog in the
	// background as it starts. For llama.cpp's router, the saved catalog holds only the models
	// that were loaded when it was saved, so a model loaded since appears once the refresh
	// lands, which took under a second against the router. Zero means five seconds.
	CatalogWait time.Duration
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
	b, err := newBridge(opts, d.CacheDir)
	if err != nil {
		return nil, err
	}
	args := slices.Clone(rpcArgs)
	if opts.Provider == llamaProvider {
		args = append(args, "-e", "builtin:"+llamaProvider)
	}
	args = append(args, b.args...)
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
	id, err := handshake(ctx, conn.client, opts, cmp.Or(d.CatalogWait, 5*time.Second))
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

// catalogPoll is how often setModel asks again for a model Pi doesn't list yet.
const catalogPoll = 100 * time.Millisecond

// setModel selects the session's model. On llama.cpp, a model Pi answers it doesn't list may
// still arrive with Pi's background catalog refresh, so setModel asks again for up to wait.
// Every other failure, such as Pi having exited or a model another provider lacks, returns at
// once.
func setModel(ctx context.Context, c *stdio.Client, opts harness.Options, wait time.Duration) error {
	cmd := command{Type: "set_model", Provider: opts.Provider, ModelID: opts.Model}
	deadline := time.Now().Add(wait)
	for {
		_, err := c.Call(ctx, cmd)
		if err == nil || !catalogMiss(opts, err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(catalogPoll):
		}
	}
}

// catalogMiss reports whether err is Pi's own answer that it doesn't list a llama.cpp model,
// the one failure a catalog refresh can still mend.
func catalogMiss(opts harness.Options, err error) bool {
	var failed *commandError
	return opts.Provider == llamaProvider && errors.As(err, &failed) &&
		strings.HasPrefix(failed.Message, "Model not found")
}

func handshake(ctx context.Context, c *stdio.Client, opts harness.Options, catalogWait time.Duration) (string, error) {
	if opts.Model != "" {
		if err := setModel(ctx, c, opts, catalogWait); err != nil {
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
