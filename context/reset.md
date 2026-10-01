# reset · harness-adapters

- **Status:** closeout
- **Session:** start
- **Branch:** harness-adapters

## Disposition

- **Integrated:** deleted `context/adapters.md`. The `claude` and `opencode` adapters, `mcpbridge`,
  and their package documentation built its plan, and `findings.md` records what each of its
  questions found. Its open ideas, a system prompt option and forcing `respond`, moved to
  `service-runtime.md`.
- **Add or sharpen:**
  - `context/findings.md`:
    - **Exchanges:** each harness's turn boundary and cancellation, and Claude Code's exit
      status 1 after a failed turn.
    - **Sessions:** resume from any directory on both new harnesses, the lost-session guard,
      and the decision to keep `harness.Journal` optional, as Pi's guard.
    - **Payloads:**
      - tools through MCP;
      - Claude Code caching a tool's schema by name, which led to `respond_<digest>` and the
        listing wait;
      - `--json-schema` rejected;
      - how each harness loads skills;
      - usage limits.
    - **Capabilities:** the quiet image drop on every harness; OpenCode's prompt confusing
      Gemma; gpt-oss's intermittent tool-then-`respond` failures.
    - **Infrastructure shape:**
      - the design held for three harnesses;
      - six modules, with `mcpbridge` as the go-sdk boundary;
      - `harness/cache`, and `stdio.Send` and `Notify`;
      - the sizes;
      - `acp-go-sdk` rejected;
      - `clutch conform`.
    - **Process lifecycle:** isolation per harness; private configuration directories;
      OpenCode's listed model; a native install updating itself.
    - **Harness comparison** (new), against the architect's two criteria, flexibility across
      model platforms and control over the harness. It holds the latency table and the
      verdict: Pi is the pinned default.
  - `context/service-runtime.md`:
    - version pins, including that a native install can't be pinned from the driver;
    - subscription usage limits;
    - compaction;
    - a `SystemPrompt` option;
    - forcing `respond`;
    - harness credentials: Pi's per-request token command, OpenCode's fixed token and its
      inheritance by OpenCode's shell tool (review finding 11c), and the MCP endpoint's token.
  - `context/README.md`: Path marks step 5 built, and names the six modules, `--harness`,
    `--provider`, and `clutch conform`.
- **Retained:** `context/service-runtime.md`, for step 6.
- **Validated:**
  - **Checkpoint 1** (live, Claude Code on Anthropic, haiku, subscription): seven of the eight
    harness scenarios passed. `vision`'s failure was the scenario's wording check rejecting
    "blue rectangle", which the architect accepted.
    - Adjust `9fe67f7`: exit status 1 after an interrupted turn.
  - **Checkpoint 2** (live, OpenCode on the router): all eight scenarios passed. Qwen3.8's
    `structured` and `vision` were run by the architect.
    - Unknown IDs fail loudly on both harnesses, and both resume from another directory.
    - Adjusts:
      - `4286aec`: each harness's dropped-image text.
      - `8dfc6b5`: the lost Claude Code session guard.
      - `2d72aac`: an OpenCode tool call with no arguments.
    - OpenCode's slowness was measured as its own per-turn overhead, not the listing wait
      (about 15 ms).
  - **Checkpoint 3** (live, Azure, keyless): Pi and OpenCode passed `exchange`, `tool`,
    `structured`, and `vision`. The direct client's 429 cleared after the architect raised the
    quota. No endpoint or token appeared in the output.
  - **Checkpoint 4** (final):
    - The editor pass ran on Sonnet (`b3caf24`).
    - Build, vet, `go test -race`, golangci-lint 2.13.2, and gofmt were clean in all six
      modules.
    - All nine Pi scenarios passed on the router from a clean `--state`.
    - `clutch conform` passed every capability on Azure and Anthropic for all three harnesses.
      The router cells failed only gpt-oss's tool-then-`respond` exchanges, and `vision` and
      `audio` when their models were unloaded.
    - Adjust `c59be83`: a latency table, at the architect's request. Pi's system prompt was
      probed and replaces entirely.
  - **Branch review** (reviewer on Opus): 11 findings, verified against the code and
    OpenCode 1.18.34's source. All were fixed in Adjust `9790418` except 11c, which is
    recorded in `service-runtime.md`.
    - Checks were clean in all six modules.
    - `clutch conform` passed every capability for Pi and OpenCode on Azure and Claude Code
      on Anthropic.
    - Claude Code had updated itself to 2.1.287, which the version gate caught. Its pin
      moved in `b000615`.

## Next-focus

Path step 6, the long-running workflow, in this repository. A workflow spans several harness
sessions, through `harness.Driver`, with:

- progress;
- event streaming to SSE;
- a concurrency limit;
- cancellation;
- resume after a restart.

The step also records the container image's footprint and checks the managed-identity and IL6
path on paper. Its validation answers the spike's question. `context/service-runtime.md` holds
the runtime notes it starts from: process cleanup, records in a service's store, token refresh,
usage limits, compaction, pinned harness versions, and a system prompt option. The harness it
runs on is open: Pi is the pinned default (`findings.md`, Harness comparison), and the
`harness` surface lets the workflow choose per session.
