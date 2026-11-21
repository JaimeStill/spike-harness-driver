# spike-harness-driver

## The question

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
container image carries a harness. The goal it serves is `experiment.harness` in the
coordinator's roadmap, `standards-lab/roadmap/index.toml`, and the strategy behind it is
`context/ai-strategy.md` in [standards-lab/org](https://github.com/standards-lab/org).

A working spike is evidence, not a decision. A `plan` session in standards-lab reads the result
and decides what the workspace takes from it. The spike's steps, its CLI, and its prior art are
in [`docs/path.md`](../docs/path.md).

## The answer

**Go can drive an external agent harness as the infrastructure for agentic work.** By part:

- **Capabilities.** Tools and skills run through every harness; a tool defined in Go reaches Pi
  through its extension and Claude Code and OpenCode through MCP. Of the native capabilities,
  harnesses expose vision alone, and drop an image a model can't take quietly. Embeddings and
  audio need a direct model client, one OpenAI-compatible shape over the router and Azure, and
  an agent reaches them through a tool ([Capabilities](../docs/findings.md#capabilities),
  [Payloads](../docs/findings.md#payloads)).
- **Sessions.** A session outlives its process on every harness and resumes by ID, keeping its
  history. Exchange IDs are the driver's own, kept in a store and bound to the harness's journal
  where it keeps one, which catches Pi's cwd-scoped sessions
  ([Sessions](../docs/findings.md#sessions)).
- **Exchanges.** No harness tags its events with a request, so a session carries one exchange at
  a time, scoped from start to settle, with each harness's cancellation mapped to one outcome. A
  structured response is a `respond` tool on every harness
  ([Exchanges](../docs/findings.md#exchanges), [Payloads](../docs/findings.md#payloads)).
- **Workflows.** A standard-library layer of about 1,800 lines over `harness.Driver` runs a DAG of
  exchanges over sessions on any mix of harnesses, with a concurrency limit, cancellation,
  progress and SSE from one log per run, and resume after an interrupt, a `kill -9`, or a
  container restart ([Workflows](../docs/findings.md#workflows)).

**What it settles for go-ai:** both surfaces, a harness-session surface and a model-client
surface, with the workflow layer above `harness` and harness-agnostic. **A service image carries
a harness:** one, pinned, Pi by default, in about 215 MB
([Deployment](../docs/findings.md#deployment),
[Harness comparison](../docs/findings.md#harness-comparison)). What a service still owes is in
[Open for a service](../docs/findings.md#open-for-a-service).

## The evidence

1. **Tools and skills.** Proven by a demo: `clutch conform`, every capability on Azure and
   Anthropic for all three harnesses. Finding: [Capabilities](../docs/findings.md#capabilities).
2. **Native capabilities.** Proven by a demo: `model` on the router and Azure, and a Go tool
   giving a Pi session a transcript. Finding: [Capabilities](../docs/findings.md#capabilities).
3. **Sessions.** Proven by a run: `clutch conform`'s `resume`, and live follow-ups from a new
   process. Finding: [Sessions](../docs/findings.md#sessions).
4. **Exchanges.** Proven by a run: `clutch conform`'s `exchange`, `cancel`, and `structured`.
   Finding: [Exchanges](../docs/findings.md#exchanges), [Payloads](../docs/findings.md#payloads).
5. **Workflows.** Proven by a demo: `clutch workflow` and `clutch serve`. Finding:
   [Workflows](../docs/findings.md#workflows).
6. **The service image** is 215 MB, idles at about 10 MiB, and adds about 95 MiB per Pi session.
   Proven by a demo: `deploy/`. Finding: [Deployment](../docs/findings.md#deployment),
   [Harness comparison](../docs/findings.md#harness-comparison).
7. **Managed identity and IL6.** The Azure managed-identity path and IL6 hold. Checked on paper
   only; nothing ran against them. Finding:
   [Managed identity and IL6, on paper](../docs/findings.md#managed-identity-and-il6-on-paper).

## Capability map

- **Session interface** (built): open, send, stream, cancel, and close, in
  [`harness`](../harness/); reasoning in [The session](../docs/design.md#the-session).
- **Session persistence** (built): keeping a session beyond one call, and resuming it, in
  [`harness/filestore`](../harness/filestore/); reasoning in
  [The record files](../docs/design.md#the-record-files).
- **Exchange scoping** (built): request-and-response block IDs bound to a session, in
  [`harness`](../harness/); reasoning in [The session](../docs/design.md#the-session).
- **Payloads** (built): encoding a request, and interpreting the response, in
  [`harness/stdio`](../harness/stdio/); reasoning in
  [The line protocol](../docs/design.md#the-line-protocol).
- **Tool calls and skills** (built): run through the harness, by
  [`mcpbridge`](../mcpbridge/), [`harness/catalog`](../harness/catalog/), and
  [`harness/cache`](../harness/cache/); reasoning in
  [Skills and command tools](../docs/design.md#skills-and-command-tools) and
  [The MCP bridge](../docs/design.md#the-mcp-bridge).
- **Native capabilities** (built): vision, embeddings, and audio, and where each needs a direct
  model client, in [`model`](../model/); reasoning in
  [The model client](../docs/design.md#the-model-client).
- **Workflow coordination** (built): long-running work over sessions, in
  [`workflow`](../workflow/), [`workflow/filestore`](../workflow/filestore/), and
  [`workflow/sse`](../workflow/sse/); reasoning in
  [The workflow runner](../docs/design.md#the-workflow-runner).
- **Adapters** (built): Pi (`pi --mode rpc`), Claude Code (`claude -p`, stream-json in both
  directions), and OpenCode (`opencode acp --pure` over stdio), in
  [`pi`](../pi/), [`claude`](../claude/), and [`opencode`](../opencode/); reasoning in
  [The Pi adapter](../docs/design.md#the-pi-adapter),
  [The Claude Code adapter](../docs/design.md#the-claude-code-adapter), and
  [The OpenCode adapter](../docs/design.md#the-opencode-adapter).
- **Model targets** (built): the Framework desktop's llama.cpp router over the tailnet, Anthropic,
  and Azure AI Foundry, set up by [`setup/`](../setup/) and picked by [`clutch`](../clutch/);
  reasoning in [The clutch CLI](../docs/design.md#the-clutch-cli).
- **Deployment** (built): the container image's footprint, in [`deploy/`](../deploy/README.md),
  and the managed-identity and IL6 path, checked
  [on paper](../docs/findings.md#managed-identity-and-il6-on-paper).
