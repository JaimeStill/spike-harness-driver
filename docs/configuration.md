# Configuration

This page gives the reasoning behind the spike's configuration files. Each file's header points
here.

## The tools and the check

[`mise.toml`](../mise.toml) pins the harness versions clutch's conformance suite
(`clutch conform`) pins. The suite skips a harness at any other version unless `--any-version` is
passed, so keep them in step with the pins in
[`clutch/internal/app/harnesses.go`](../clutch/internal/app/harnesses.go). Pi and OpenCode are
held at the versions `clutch conform` measured, each with its reason beside the pin.

Claude Code is absent from `mise.toml` because it is a native install, which the driver keeps from updating itself
(`DISABLE_AUTOUPDATER`), so the version on the PATH is the one the suite sees.

node runs the Pi bridge's TypeScript tests in the check (`node --test` strips the types). go and jq
are pinned because the check and currency call them. marathon's release,
`github:standards-lab/claude-plugins` at its `marathon/v` tag, provides lint-context and
lint-references, which the check runs. Tasks never install a missing tool
(`task.run_auto_install = false`). Of the tasks, only `currency` reads the network; the check reads
only the Go module proxy on a cold cache.

`mise run check` ([`scripts/check.sh`](../scripts/check.sh)) builds, vets, format-checks, and
race-tests every module in `go.work`, runs the Pi bridge's TypeScript tests, and runs
lint-context and lint-references. Every step runs, and the check fails if any failed; a lint
missing from the PATH fails it with `FAIL: <lint> is not on PATH; run mise install`. No test
reaches a router, a harness, or the network, and the check writes nothing.

## Currency

`mise run currency` ([`scripts/currency.sh`](../scripts/currency.sh)) reports every `mise.toml` and
`go.mod` pin behind its latest and exits nonzero if any trail: each mise tool, a newer marathon tag
included, and each `go.work` module's direct requirements. A pin with a comment beside it in
`mise.toml` is held, and the comment says why; a held pin that trails is still reported, marked
`(held)`, as Pi's and OpenCode's are.

- The requirements are read in workspace mode, as the modules build: with `GOWORK=off`, opencode
  fails on a go.sum entry that only mcpbridge's go.sum holds.
- A requirement's latest is the module proxy's newer release, or else the version the workspace
  selects, so a pin another workspace module has outgrown is reported too.
- The workspace's own modules are not requirements, and are skipped.

## References

[`references.toml`](../references.toml) lists the repositories this spike reads. Each key maps
to the canonical remote for one repository. `references.local.toml` (not committed) maps the same
key to its directory on this machine. The keys match the standards-lab references catalog.

- **tau** (`tau-protocol`, `tau-format`, `tau-provider`, `tau-agent`, `tau-orchestrate`,
  `tau-platform`): the organization's earlier agentic infrastructure.
- **`tau-agent`**: the Chat, Vision, and Embed client, prior art for native capabilities, not a
  baseline.
- **`tau-orchestrate`**: the state graph, prior art for workflow coordination.
- **`tau-platform`**: holds `archive/tau-runtime/claude-classify-docs`, a workflow run by a
  harness alone.
- **`herald`**: the workload that shaped tau. Prior art only.

## marathon

[`.claude/marathon.toml`](../.claude/marathon.toml) sets the project kind: this repository holds
Go spike source with a build-and-test loop, checked by `mise run check`, with its currency
reported by `mise run currency`. Its remote section names the platform, the command that
publishes a finished branch, and the command that merges it. The repository has no CI, so the
merge waits on no pull-request checks.

## Untracked files

[`.gitignore`](../.gitignore) keeps out of the repository:

- marathon's session plan files and briefs;
- the branch review's report, `.claude/report.md`, read by the architect and deleted at closeout;
- the per-machine local directory map for `references.toml`, `references.local.toml`;
- build and coverage artifacts;
- connection details and secrets for model targets
  ([`setup/azure-foundry.md`](../setup/azure-foundry.md)).
