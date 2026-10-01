# Adapters

Planned for step 5. Each adapter is a `harness.Connection`.

- **Claude Code**: `claude -p --input-format stream-json --output-format stream-json
  --include-partial-messages`, over a `stdio.Client`. Its interrupt is a control request
  correlated by `request_id`, which a Codec maps to a `stdio.Response`.
- **OpenCode**: `opencode acp`, which speaks JSON-RPC over stdio, also over a `stdio.Client`. The
  alternative is its server API over HTTP and SSE, which would need a second transport.
- If a harness tags its events with a request ID, as OpenCode's may, the session could route by
  ID instead of allowing one open exchange. Build that only if a harness needs it.

- **Persistence.** For each harness, find out:
  - whether it keeps a durable journal of a session with stable entry IDs, as Pi's
    `get_entries` does. Claude Code's session JSONL files and OpenCode's message IDs are the
    candidates to check.
  - whether it scopes a session ID to the working directory, as Pi does.

  Then `harness.Journal` becomes required, or is dropped (`findings.md`, Sessions).

- **Tools.** Go tools reach Pi through the bridge's dialogs. For the others:
  - Claude Code: probably `control_request` messages of subtype `mcp_message`, which the Agent
    SDK uses for in-process tools, answered through `stdio`'s request path. Unverified.
  - OpenCode: the `mcpServers` that ACP's `session/new` takes, which needs an MCP server the
    driver runs or starts.

  Then decide whether one Go MCP server (`modelcontextprotocol/go-sdk`) serves every harness,
  Pi included through its bridge, or each harness keeps its own route. A command tool's
  `tool.json` already uses MCP's `inputSchema`, so a manifest maps to an MCP tool.
- **Structured responses.** Pi's `respond` is a tool the prompt asks for. Forcing it with the
  provider's `tool_choice`, through Pi's `before_provider_request` hook, would be sturdier, but
  the payload's shape differs per provider. A `respond` call in the same batch as another tool
  doesn't end the run, and a second one replaces the first.
- **The surface.** Pi's mechanics show through `harness`: `respond` arrives as ordinary tool
  events, so a validation failure can only be found by the tool's name, and `Usage` is the last
  message's, which undercounts an exchange with tool calls. With a second adapter, fold
  `respond` into `EventStructured` plus a validation-failure event, and sum `Usage` over the
  exchange.

- **Native capabilities.** For each harness, find out whether it takes images, and how it
  decides a model takes them. Pi decides from its provider catalog and drops an image quietly
  for a model without image input (`findings.md`, Capabilities).
- **Model catalogs.** Pi selects a model from a catalog it saved and refreshes in the background
  (`findings.md`, Process lifecycle). Find out how each harness learns a provider's models, and
  whether a newly loaded model is selectable at once.
- **A cloud harness target.** Pi has run only against the router. The conformance suite runs
  each harness against a cloud target too, such as Azure through Pi's own provider.
- **The suite's checks.** The capability scenarios check a reply with heuristics that can
  misjudge it: a text-only model's stereotypical guess at the shapes fails the dropped-image
  check, and the shape check accepts a negation. A conformance suite prefers checks that don't
  depend on a model's wording.

Assumes both harnesses accept a new prompt only after the previous run ends, as Pi does.
