# spike-harness-driver

## Question

Can Go drive an external agent harness (Pi, Claude Code, or OpenCode, against local and cloud
models) as the infrastructure for agentic work? And what does that infrastructure need to be?
The question has four parts:

- **Capabilities.** Which of skills, tool calls, and the native model capabilities beyond chat
  (vision, embeddings, audio) a harness exposes, and which ones need a direct model client.
- **Sessions.** Establishing and managing work that spans several agent turns, keeping a session
  beyond a single call, and resuming it.
- **Exchanges.** Sending a payload and interpreting the response payload. Scoping one
  request-and-response block, and identifying it against a persistent session.
- **Workflows.** Coordinating long-running workflows over those sessions: progress, event
  streaming, cancellation, and concurrency.

## The decision it changes

The answer settles go-ai's shape: a harness-session surface only, both a harness-session and a
model-client surface, or a model-client surface only. It also settles whether a service's
container image carries a harness. The goal it serves is `v1.ai` in the standards-lab roadmap,
and the strategy behind it is `context/ai-strategy.md` in
[standards-lab/org](https://github.com/standards-lab/org).

A working spike is evidence, not a decision. A `plan` session in standards-lab reads the result
and decides what the workspace takes from it.

## Capability map

- **Session interface**: open, send, stream, cancel, and close
- **Session persistence**: keeping a session beyond one call, and resuming it
- **Exchange scoping**: request-and-response block IDs bound to a session
- **Payloads**: encoding a request, and interpreting the response
- **Tool calls and skills** run through the harness
- **Native capabilities**: vision, embeddings, and audio, and where each needs a direct model
  client
- **Workflow coordination**: long-running work over sessions
- **Adapters**: Pi (`--mode rpc` or `json`), Claude Code (`claude -p --output-format
  stream-json`, or the Agent SDK), and OpenCode (its server API, or `opencode acp` over stdio)
- **Model targets**: the Framework desktop's llama.cpp router over the tailnet, Anthropic, and
  Azure AI Foundry
- **Deployment**: the container image's footprint, and the managed-identity and IL6 path,
  checked on paper

## Path

The spike's sessions own these steps, in dependency order, and may revise them. The session
interface and the Pi adapter (step 1), session persistence and resume (step 2), payloads, tool
calls, and skills (step 3), native capabilities through a direct model client, `model` (step 4),
and the Claude Code and OpenCode adapters with a conformance suite (step 5) exist.
`context/findings.md` records what they showed, and compares the three harnesses. The code is
six modules under `go.work`: the root module (the `harness/...` core and `model`),
`mcpbridge`, the adapters `pi`, `claude`, and `opencode`, and `clutch`.

`clutch` (`go run ./clutch/cmd/clutch`) drives them. `--harness pi|claude|opencode` picks the
harness, and `--provider` its models (`llama.cpp` for the router, `azure` for Azure AI Foundry,
`anthropic` for Claude Code). Its `scenario` commands each show one capability, its `session`
commands run one exchange at a time, and `conform` runs every capability over every harness
and provider as a matrix, against pinned harness versions. `--target` points the direct client
at the router or Azure AI Foundry, which `setup/` sets up.

6. **Long-running workflow.** A workflow spans several sessions, with progress, event streaming to
   SSE, a concurrency limit, cancellation, and resume after a restart. The step also records the
   container image's footprint and checks the managed-identity and IL6 path on paper. Its
   validation answers the question.

## Prior art

`references.toml` lists the repositories this spike reads. tau's `agent` package (Chat, Vision,
and Embed) is prior art for native capabilities, not a baseline to adopt: it has no audio, among
other gaps, so it informs the spike's own client rather than supplying it. tau's `orchestrate`
package is the prior art for workflows. `claude-classify-docs`, in tau-platform, showed that a harness can
run a real workflow with no Go infrastructure. herald is the workload that shaped tau.
