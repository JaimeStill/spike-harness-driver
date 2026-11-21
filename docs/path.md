# Path

The steps the spike took, the code they built, and the repositories it reads.

The spike's steps, in dependency order, all exist: the session interface and the Pi adapter
(step 1), session persistence and resume (step 2), payloads, tool calls, and skills (step 3),
native capabilities through a direct model client, `model` (step 4), the Claude Code and
OpenCode adapters with a conformance suite (step 5), and a long-running workflow over sessions
with a container image (step 6). [`findings.md`](findings.md) records what they showed and
compares the three harnesses, and [`context/README.md`](../context/README.md#the-answer) states
the answer. The code is six modules under `go.work`: the root module (the `harness/...` core,
`model`, and the `workflow/...` layer), `mcpbridge`, the adapters `pi`, `claude`, and `opencode`,
and `clutch`.

`clutch` (`go run ./clutch/cmd/clutch`) drives them. `--harness pi|claude|opencode` picks the
harness, and `--provider` its models (`llama.cpp` for the router, `azure` for Azure AI Foundry,
`anthropic` for Claude Code). Its `scenario` commands each show one capability, its `session`
commands run one exchange at a time, and `conform` runs every capability over every harness
and provider as a matrix, against pinned harness versions. Its `workflow` commands run a
workflow file to its end and resume one left unfinished, and `serve` runs workflows over HTTP
with their events as SSE. `--target` points the direct client at the router or Azure AI
Foundry, which `setup/` sets up. `deploy/` builds the container image.

## Prior art

`references.toml` lists the repositories this spike reads. tau's `agent` package (Chat, Vision,
and Embed) is prior art for native capabilities, not a baseline to adopt: it has no audio, among
other gaps, so it informs the spike's own client rather than supplying it. tau's `orchestrate`
package is the prior art for workflows. `claude-classify-docs`, in tau-platform, showed that a harness can
run a real workflow with no Go infrastructure. herald is the workload that shaped tau.
