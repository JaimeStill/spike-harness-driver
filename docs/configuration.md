# Configuration

The reasoning behind the spike's configuration files, whose headers point here.

## The tools and the check

[`mise.toml`](../mise.toml) pins the harness versions clutch's conformance suite
(`clutch conform`) pins. The suite skips a harness at any other version unless `--any-version` is
passed, so keep them in step with the pins in
[`clutch/internal/app/harnesses.go`](../clutch/internal/app/harnesses.go). Pi and OpenCode are
held at the versions `clutch conform` measured, each with its reason beside the pin.

Claude Code is absent because it is a native install, which the driver keeps from updating itself
(`DISABLE_AUTOUPDATER`), so the version on the PATH is the one the suite sees.

node runs the Pi bridge's TypeScript tests in the check (`node --test` strips the types).

`mise run check` builds, vets, format-checks, and race-tests every module in `go.work`, then runs
the Pi bridge's TypeScript tests. It is hermetic: no test reaches a router, a harness, or the
network, and it writes nothing.

## References

[`references.toml`](../references.toml) lists the repositories this spike reads. Each key maps to
the canonical remote for one repository. `references.local.toml` (not committed) maps the same key
to its directory on this machine. The keys match the standards-lab references catalog.

These repositories are read-only for this spike. Read them, and never write to them. Code
dependencies are published versions, never a `replace` directive.

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
Go spike source with a build-and-test loop, checked by `mise run check`. Its remote section names
the platform, the command that publishes a finished branch, and the command that merges it. The
repository has no CI, so the merge waits on no pull-request checks.

## Untracked files

[`.gitignore`](../.gitignore) keeps out of the repository:

- marathon's session plan files and briefs;
- the branch review's report, `.claude/report.md`, read by the architect and deleted at closeout;
- the per-machine local directory map for `references.toml`, `references.local.toml`;
- build and coverage artifacts;
- connection details and secrets for model targets
  ([`setup/azure-foundry.md`](../setup/azure-foundry.md)).
