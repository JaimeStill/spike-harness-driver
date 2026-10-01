package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/workflow"
	"github.com/JaimeStill/spike-harness-driver/workflow/filestore"
)

// sessionOpenTimeout bounds a workflow session's start-up handshake, as the session domain's
// bounds its own.
const sessionOpenTimeout = 30 * time.Second

// WorkflowRunner returns a Runner that logs its runs under --state and opens each session of a
// workflow through an Infrastructure of its own, so a session runs on the harness, provider,
// and model it names. limit bounds the exchanges in flight; zero is no limit.
//
// Every session's harness works in one directory under --state, whatever the process's working
// directory: Pi scopes a session ID to the directory it runs in, and a run resumed from another
// directory would otherwise reopen its sessions fresh.
func (i *Infrastructure) WorkflowRunner(limit int) (*workflow.Runner, error) {
	dir := filepath.Join(i.cfg.State, "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	store := filestore.New(filepath.Join(i.cfg.State, "workflows"))
	return workflow.NewRunner(i.sessionOpener(dir), store, limit), nil
}

// sessionOpener opens a workflow's sessions in dir. Each session's Infrastructure is built from
// a copy of the flags with the session's harness, provider, and model, the way the conformance
// suite builds a cell's, so the per-harness defaults and the azure provider's setup apply as
// they do for every other command. The session enables none of the harness's own tools: a
// workflow session offers the model the --tools and --skills sessions offer, and nothing that
// reaches the service's files or shell.
func (i *Infrastructure) sessionOpener(dir string) workflow.Opener {
	return func(ctx context.Context, spec workflow.SessionSpec, id string) (*harness.Session, error) {
		cfg := sessionConfig(*i.cfg, spec)
		infra := newInfrastructure(&cfg)
		if err := infra.Validate(); err != nil {
			return nil, err
		}
		d, err := infra.Driver()
		if err != nil {
			return nil, err
		}
		opts := infra.Options()
		opts.SessionID, opts.Dir, opts.HarnessTools = id, dir, []string{}
		ctx, cancel := context.WithTimeout(ctx, sessionOpenTimeout)
		defer cancel()
		sess, err := d.Open(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("%s on %s/%s: %w", spec.Name, cfg.Harness, infra.provider(), err)
		}
		return sess, nil
	}
}

// sessionConfig is base with a workflow session's harness, provider, and model. A harness other
// than the flags' takes its own provider and models unless the session names them, and a
// provider other than the flags' its own model, since a model ID belongs to one provider.
func sessionConfig(base Config, spec workflow.SessionSpec) Config {
	cfg := base
	if spec.Harness != "" && spec.Harness != cfg.Harness {
		cfg.Harness, cfg.Provider, cfg.Model, cfg.HarnessVisionModel = spec.Harness, "", "", ""
	}
	if spec.Provider != "" && spec.Provider != newInfrastructure(&cfg).provider() {
		cfg.Provider, cfg.Model, cfg.HarnessVisionModel = spec.Provider, "", ""
	}
	if spec.Model != "" {
		cfg.Model = spec.Model
	}
	return cfg
}
