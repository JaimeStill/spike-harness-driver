# Findings

Evidence toward the spike's question, by part. The coordinator's `plan` session reads it with
the result. The package documentation in `harness`, `harness/stdio`, `harness/filestore`,
`harness/catalog`, and `pi` explains how the built code works; this note holds what the code
shows about harnesses in general.

## Exchanges

- Pi's RPC events carry no request ID. Only command responses echo the client's `id`. An
  exchange is therefore the window from `agent_start` to `agent_settled`, and a session carries
  one exchange at a time. `agent_end` doesn't end an exchange, because a retry, a compaction, or
  a queued message can start another run after it.
- A `prompt` response means only that Pi accepted the prompt. The run's outcome, including any
  error, arrives as events.
- Pi answers `abort` after the run settles. Pi keeps queued messages through an abort, so a
  cancellation sends `clear_queue` first. Pi cancels in two round trips, and the exchange can end
  between them, so a cancellation in flight holds off the next exchange.
- A cancellation can land where Pi sends no aborted message, such as during a tool call. The
  session therefore records the cancellation itself, rather than trusting the harness to report
  it.
- The driver assigns exchange IDs itself (UUIDv7), because the harness has none to offer.

## Sessions

- Pi persists a session by default. `--session-id` opens a session by ID in a new process, or
  creates it, and the resumed session keeps its history: a follow-up in the new process answers
  from the earlier exchange.
- Pi scopes a session ID to the working directory, even with `--session-dir`. Resuming from
  another directory silently starts a fresh session under the same ID.
- Pi's `get_entries` gives each session entry a stable ID that survives the process, and
  `since` works as a cursor. It fails when `since` names no entry.
- Opening a session appends entries of its own. Setting the model appends three entries on a new
  session (`model_change`, `thinking_level_change`, `model_change`) and one on a resumed session.
  A session's first run appends a system message before the user message.
- A session's history names the files the harness read, such as a skill's `SKILL.md`, so those
  files must outlive the process. A skill on disk is loaded where it lives, and anything else
  the driver loads from files sits in a cache under a name its content decides. A session
  resumed in a new process re-read its skill at the path in its history.
- Exchange IDs stay the driver's own, because Pi has none to offer. They survive a resume
  through a `Store` of records, and each record is bound to the journal entries its exchange
  appended. The binding is what makes a lost or wrong session detectable:
  - On resume, the session checks that the journal still holds the last recorded entry. A
    missing entry is `ErrJournalMismatch`, which is how the cwd-scoped fresh session surfaces.
  - The check covers only the last entry.
  - The check isn't read-only, because opening Pi appends entries.
- Rejected ways to carry exchange IDs:
  - **A Pi extension** that writes the ID into Pi's own session with `pi.appendEntry`. It works
    for Pi alone. The driver now loads an extension of its own (Payloads), so the extension
    itself is no longer the cost; the approach stays rejected because no other harness has an
    equivalent.
  - **Pi's entry ID as the exchange ID.** The ID would be known only after the prompt lands, and
    would be specific to the harness.
  - **Driver records with no binding to the harness.** The records could drift from what the
    harness holds, with nothing to detect it.
  - **`switch_session`**, which loads a session file by path inside a running Pi. It suits a
    process-per-session driver less well than `--session-id` given at start.

## Payloads

- Pi's RPC registers no tools and loads no skills. A tool reaches Pi only through an extension,
  and a skill only through `--skill`. Explicit `-e` and `--skill` paths still load with discovery
  off (`-ne`, `-ns`), so the driver keeps the user's own extensions and skills out while loading
  its own.
- The driver's baseline for Pi is one extension, the bridge. It registers the session's tools
  and asks the driver to run each call through an input dialog (`extension_ui_request`), which
  the driver answers on the same RPC stream. Tools defined in Go, including command tools in any
  language, ran end to end this way, with no second transport.
- Pi lists skills to the model only when its `read` or `bash` tool is active; the model then
  reads `SKILL.md` itself. A prompt of `/skill:<name>` inlines the skill with no tool at all.
- Pi has no provider-level response schema. A structured response is the arguments of a
  terminating tool, `respond`, which Pi validates against the exchange's schema. Registering
  `respond` again per exchange gives each exchange on one session its own shape, and
  `terminate: true` skips the follow-up model turn.
- Pi tells the model about a tool added partway through a session only as a `toolsAdded` delta
  with its name and schema, without its prompt guidelines. With another tool active, gpt-oss
  then answered in text instead of calling `respond`. An instruction appended to the prompt made
  3 of 3 runs, and every later run, call it.
- With a prompt that contradicted that instruction ("plain text only, without calling any
  tool"), gpt-oss on llama.cpp reasoned that the system instruction wins and called `respond`,
  but in about half the runs llama.cpp rejected the call ("The model produced output that does
  not match the expected peg-native format"). Pi records that as `stopReason: "error"` and
  doesn't retry it.
- The validation-retry path, a `respond` call Pi rejects and the model retries, hasn't been seen
  live: gpt-oss met every schema on its first call, including a regex pattern and exact item
  counts.
- A warm prompt cache takes most of the input: one exchange reported `input` 37 and `cacheRead`
  745. `Usage` counts both.

## Capabilities

- gpt-oss-120b on the llama.cpp router streams thinking deltas, although Pi's model catalog marks
  it `reasoning: false`.
- The router loads models on demand and lists some as unloaded. An exchange against a cold model
  can take minutes to produce its first token (`service-runtime.md`).

## Infrastructure shape

This design is provisional until the Claude Code and OpenCode adapters (step 5) use it.

- Three modules under a committed `go.work`, the packaging recommended for go-ai:
  - the core (`harness`, `harness/stdio`, `harness/filestore`, `harness/catalog`);
  - one module per adapter (`pi`), which carries its harness's baseline;
  - the programs that consume them (`clutch`).

  The go.mod files carry no `require` lines for workspace modules, since a placeholder version
  fails once a module also requires a real dependency. The cost is that `pi` and `clutch` build
  only inside the workspace.
- The layers:
  - `harness` is transport-agnostic. It holds exchange scoping, sequencing, result folding,
    cancellation, exchange records, and the tool, skill, and schema types, over a five-method
    `Connection`. About 920 lines.
  - `harness/catalog` loads tools and skills from outside the program: skill trees from any
    `fs.FS` or from disk, and command tools from `tool.json` manifests. About 490 lines.
  - `harness/stdio` is the transport for line-protocol harnesses. It also answers requests the
    harness makes of the driver, the shape Claude Code's control requests take. About 570 lines.
  - `pi` is the adapter: about 920 lines of Go and the 100-line `bridge.ts`.
- Persistence adds two interfaces to `harness`:
  - `Store` keeps exchange records. `harness/filestore` is the spike's implementation, one JSON
    Lines file per session.
  - `Journal` is optional for a `Connection`: `Head`, and `Since(id)`. Step 5 either makes it
    required, if every harness keeps a journal with stable entry IDs, or drops it.
- The driving program, `clutch`, follows the Elemental CLI layout, which the Standards Lab
  note `context/cli-applications.md` records. Its scenarios each show one capability
  (`exchange`, `cancel`, `resume`, `tool`, `skill`, `structured`), and its `session` commands run
  one exchange or list the records, one process per call. `--tools` and `--skills` load external
  sources, and `clutch/examples` holds one of each.
- Assumes Claude Code (`stream-json` with control requests) and OpenCode (`opencode acp`) fit
  `stdio` as Pi does. `adapters.md` holds the plan.

## Process lifecycle

- A driver that runs a harness as a process owns everything the harness starts. The harness runs
  in a process group of its own, and what is left of the group is killed when the harness exits.
  `service-runtime.md` covers the rejected alternatives.
- The same holds for a command tool: it runs in a process group of its own, which is killed once
  the command exits, so a script's leftover child neither outlives the call nor holds it open.
- The harness's stdout and stderr must be `*os.File` pipes. Any other writer makes exec copy it
  through a goroutine that `Wait` waits for, and a leftover child holding the pipe then holds up
  the exit.
- Line protocols are split on LF only. A JSON string may hold U+2028, and a line may exceed any
  fixed buffer.
- The driver's cache holds code the harness runs, the bridge, so it must belong to the current
  user and be writable by no one else. A directory under a shared temporary directory would let
  another local user plant a bridge under the name the driver trusts.
