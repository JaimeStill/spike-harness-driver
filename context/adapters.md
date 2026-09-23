# Adapters

Planned for step 5. Each adapter is a `harness.Connection`.

- **Claude Code**: `claude -p --input-format stream-json --output-format stream-json
  --include-partial-messages`, over a `stdio.Client`. Its interrupt is a control request
  correlated by `request_id`, which a Codec maps to a `stdio.Response`.
- **OpenCode**: `opencode acp`, which speaks JSON-RPC over stdio, also over a `stdio.Client`. The
  alternative is its server API over HTTP and SSE, which would need a second transport.
- If a harness tags its events with a request ID, as OpenCode's may, the session could route by
  ID instead of allowing one open exchange. Build that only if a harness needs it.

Assumes both harnesses accept a new prompt only after the previous run ends, as Pi does.
