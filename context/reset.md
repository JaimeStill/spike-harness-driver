# reset · payloads-tools-skills

- **Status:** closeout
- **Session:** start
- **Branch:** payloads-tools-skills

## Disposition

- **Integrated:** deleted `context/payloads.md`. Its three items are built, and the `harness`
  package documentation covers them: `Event.Err` is an `error`, `Connection.Err` reports the
  exit error, and `Usage` counts cache reads and writes.
- **Add or sharpen:**
  - `context/findings.md`:
    - A new Payloads section records how tools, skills, and structured responses reach Pi:
      - the bridge baseline and its dialog callbacks;
      - skills listed to the model only with `read` or `bash` active;
      - the `toolsAdded` delta that drops a tool's guidelines, and the prompt instruction it
        requires;
      - the peg-native parse failure under a prompt that contradicts the instruction;
      - the unseen validation-retry path;
      - cache usage.
    - Sessions records that skill paths are stable across a resume.
    - The rejected Pi-extension option is restated: the bridge removes its cost, and it stays
      rejected because it works for Pi alone.
    - Infrastructure shape records the three-module layout as go-ai's packaging, and its
      workspace-only cost, with `harness/catalog`, `stdio`'s requests, and new line counts.
    - Process lifecycle adds command-tool process groups and the private cache.
  - `context/adapters.md`, for step 5:
    - how Go tools reach each harness, and whether one Go MCP server serves them all;
    - `tool_choice` as a sturdier way to force `respond`;
    - review finding 10: fold `respond` into `EventStructured` with a validation-failure event,
      and sum `Usage` over the exchange.
  - `context/service-runtime.md`, for step 6:
    - a database registry of tools and skills;
    - layered skill search paths with precedence, at the architect's request;
    - the command tools' environment, the `defaultTools` default, and cache leftovers;
    - model cold starts, and provider errors Pi doesn't retry;
    - the private default `--state`.
  - `context/README.md`:
    - Path marks step 3 built, and names `go run ./clutch/cmd/clutch`.
    - Step 4 is the spike's own code under the Elemental Architecture.
    - Prior art recasts tau's `agent` as prior art, not a baseline, at the architect's
      direction. `references.toml`'s comment matches.
- **Retained:** `context/adapters.md` (step 5) and `context/service-runtime.md` (step 6).
- **Validated:**
  - **Checkpoint 1** (Go tools and skills, live on the llama.cpp router with gpt-oss-120b):
    - `scenario tool`: the model called the Go tool `lookup_code`, whose handler returned
      `ZX-2BD806`, and replied with it. With `--tools clutch/examples/tools`, it called the
      python command tool `fingerprint` (`c278be9e2247`, matched in Go).
    - `scenario skill`: the embedded `clutch-motto` skill ran through `/skill:` with no tools,
      and was found by the model with only `read` active. With `--skills
      clutch/examples/skills`, the model found `clutch-weather`.
    - `exchange`, `cancel`, and `resume` passed, and no process was left.
    - Adjust `464a9c3` gave skills and the bridge stable paths: on-disk skills in place, the rest
      in a content-addressed cache. A session resumed in a new process re-read its skill at the
      path in its history.
  - **Checkpoint 2** (structured responses, live): `scenario structured` decoded `{city,
    country}` and `{count, colors}` on one session, twice. `session send --schema` met a
    pattern and exact-count schema on its first call.
  - **Checkpoint 3** (final validation):
    - The editor pass ran on Sonnet (`1e7224d`).
    - Build, vet, `go test -race`, and golangci-lint 2.13.2 passed across all three modules.
    - All six scenarios ran live from a clean `--state`.
    - `structured` step 4 failed in 2 of 4 runs: its prompt contradicted the schema instruction,
      and llama.cpp rejected gpt-oss's malformed tool call. Adjust `d247a4f`, at the
      architect's direction, asks for the greeting directly. It then passed 4 of 4.
  - **Branch review** (reviewer on Opus). Findings 1–9, 11, and a leaking test were fixed in
    Adjust `2f31a1a`:
    - a private, digest-checked cache, and the default `--state` in the user's cache
      directory;
    - command-tool process groups killed after every exit;
    - the session's own cancel mark;
    - a bounded wait for tool answers at shutdown;
    - schema and tool-name validation in `harness`;
    - the `respond` sentinel;
    - the exit cause captured at `Wait`;
    - PID-checked group-kill tests;
    - Pi's skill-name rule.

    The new tests for findings 2, 3, and 7 fail with their fixes reverted. The suite and lint
    passed, and `tool`, `skill`, and `structured` passed live again. Finding 10 is recorded in
    `adapters.md` for step 5.

## Next-focus

Path step 4, native capabilities, in this repository.

- Establish which of vision, embeddings, and audio Pi exposes to a driver: image input through
  RPC `prompt` (Pi's `@image` files and message content), and whether it offers anything for
  embeddings or audio.
- Build a thin direct model client for what the harness doesn't expose, covering vision,
  embeddings, and audio, against the llama.cpp router and a cloud target.
- Write it as the spike's own Go infrastructure under the Elemental Architecture
  ([standards-lab/architecture](https://github.com/standards-lab/architecture): `architecture.md`,
  its principles, especially minimal-footprint and downward-dependencies, and the Go Elemental
  standard).
- tau's `agent` package (Chat, Vision, Embed) is prior art that informs the client's shape, not
  a baseline to adopt: it has no audio, among other gaps.
- Each capability gets its own `clutch` scenario.

The design question to settle first: where the direct client sits beside `harness`. It could be
a sibling module the same programs compose, or a capability a harness session can also reach,
for example as a tool. It also depends on which native capabilities the router's models serve.
