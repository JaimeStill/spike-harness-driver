# Findings

Evidence toward the spike's question, by part. The coordinator's `plan` session reads it with
the result. The package documentation in `harness`, `harness/stdio`, `harness/filestore`, and
`pi` explains how the built code works; this note holds what the code shows about harnesses in
general.

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
- Exchange IDs stay the driver's own, because Pi has none to offer. They survive a resume
  through a `Store` of records, and each record is bound to the journal entries its exchange
  appended. The binding is what makes a lost or wrong session detectable:
  - On resume, the session checks that the journal still holds the last recorded entry. A
    missing entry is `ErrJournalMismatch`, which is how the cwd-scoped fresh session surfaces.
  - The check covers only the last entry.
  - The check isn't read-only, because opening Pi appends entries.
- Rejected ways to carry exchange IDs:
  - **A Pi extension** that writes the ID into Pi's own session with `pi.appendEntry`. It works
    for Pi alone, and puts TypeScript in the Go module. Discovery stays off (`-ne`), because
    discovery would load the user's own extensions.
  - **Pi's entry ID as the exchange ID.** The ID would be known only after the prompt lands, and
    would be specific to the harness.
  - **Driver records with no binding to the harness.** The records could drift from what the
    harness holds, with nothing to detect it.
  - **`switch_session`**, which loads a session file by path inside a running Pi. It suits a
    process-per-session driver less well than `--session-id` given at start.

## Capabilities

- gpt-oss-120b on the llama.cpp router streams thinking deltas, although Pi's model catalog marks
  it `reasoning: false`.
- With the router's prompt cache warm, Pi reports an input usage of 1 token. `payloads.md` covers
  the consequence.

## Infrastructure shape

This design is provisional until the Claude Code and OpenCode adapters (step 5) use it.

- Three layers:
  - `harness` is transport-agnostic. It holds exchange scoping, sequencing, result folding,
    cancellation, and exchange records, over a four-method `Connection`.
  - `harness/stdio` is the transport for line-protocol harnesses.
  - `pi` is the adapter. It is about 400 lines of Pi-specific code, against about 1,300 lines
    of reusable layers.
- Persistence adds two interfaces to `harness`:
  - `Store` keeps exchange records. `harness/filestore` is the spike's implementation, one JSON
    Lines file per session.
  - `Journal` is optional for a `Connection`: `Head`, and `Since(id)`. Step 5 either makes it
    required, if every harness keeps a journal with stable entry IDs, or drops it.
- The driving program, `clutch`, follows the Elemental CLI layout, which the Standards Lab
  note `context/cli-applications.md` records. Its scenarios each show one capability
  (`exchange`, `cancel`, `resume`), and its `session` commands run one exchange or list the
  records, one process per call.
- Assumes Claude Code (`stream-json` with control requests) and OpenCode (`opencode acp`) fit
  `stdio` as Pi does. `adapters.md` holds the plan.

## Process lifecycle

- A driver that runs a harness as a process owns everything the harness starts. The harness runs
  in a process group of its own, and what is left of the group is killed when the harness exits.
  `service-runtime.md` covers the rejected alternatives.
- The harness's stdout and stderr must be `*os.File` pipes. Any other writer makes exec copy it
  through a goroutine that `Wait` waits for, and a leftover child holding the pipe then holds up
  the exit.
- Line protocols are split on LF only. A JSON string may hold U+2028, and a line may exceed any
  fixed buffer.
