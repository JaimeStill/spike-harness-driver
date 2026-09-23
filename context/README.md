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
  stream-json`, or the Agent SDK), and OpenCode (its server API)
- **Model targets**: the Framework desktop's llama.cpp router over the tailnet, Anthropic, and
  Azure AI Foundry
- **Deployment**: the container image's footprint, and the managed-identity and IL6 path,
  checked on paper

## Path

The spike's sessions own these steps, in dependency order, and may revise them:

1. **Session interface and Pi adapter.** Pi runs in `--mode rpc` against the Framework router. One
   session carries two scoped exchanges, and one exchange is cancelled mid-stream.
2. **Persistence and resume.** A session outlives one call and resumes by ID, and its exchange IDs
   survive the resume.
3. **Payloads, tool calls, and skills.** Register a tool and a skill, observe the tool-call
   events, and interpret a structured, schema-validated response.
4. **Native capabilities.** Establish which of vision, embeddings, and audio the harness exposes,
   and build a thin direct model client for the rest.
5. **Claude Code and OpenCode adapters.** A conformance suite runs the three adapters behind one
   interface, against local and cloud targets.
6. **Long-running workflow.** A workflow spans several sessions, with progress, event streaming to
   SSE, a concurrency limit, cancellation, and resume after a restart. The step also records the
   container image's footprint and checks the managed-identity and IL6 path on paper. Its
   validation answers the question.

## Prior art

`references.toml` lists the repositories this spike reads. tau's `agent` package supplies the
baseline for native capabilities (Chat, Vision, and Embed), and tau's `orchestrate` package is
the prior art for workflows. `claude-classify-docs`, in tau-platform, showed that a harness can
run a real workflow with no Go infrastructure. herald is the workload that shaped tau.
