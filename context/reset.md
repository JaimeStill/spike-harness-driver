# reset · session-persistence-resume

- **Status:** closeout
- **Session:** start
- **Branch:** session-persistence-resume

## Disposition

- **Add or sharpen:**
  - `context/findings.md`:
    - Sessions now records what step 2 showed:
      - Pi persists sessions and resumes one by `--session-id` in a new process.
      - A session ID is scoped to the working directory, even with `--session-dir`.
      - `get_entries` entry IDs are stable, and `since` works as a cursor.
      - Opening a session appends entries of its own.
      - Exchange IDs survive a resume in a `Store`, bound to the journal. The resume check
        covers only the last recorded entry, and it isn't read-only.
      - The rejected ways to carry exchange IDs.
    - Infrastructure shape adds `Store`, the optional `Journal`, and `clutch`'s layout, with
      updated line counts.
  - `context/README.md`: Path drops step 2 and names `clutch`.
  - `context/adapters.md`: step 5 finds out, for each harness, whether it keeps a journal with
    stable IDs and whether its session IDs are cwd-scoped. The answer makes `Journal` required,
    or drops it.
  - `context/service-runtime.md`: exchange records at service scale:
    - a database `Store`
    - a torn last line blocks a resume
    - synchronous recording delays the next `Send`
    - `Head` reads every entry
    - the temporary `--state`
- **Retained:** `context/payloads.md`, for step 3.
- **Cross-repo** (at the architect's direction, in the Standards Lab workspace):
  - Worktree `~/architecture/standards-lab/.claude/worktrees/cli-application-type`, branch
    `cli-application-type` from `origin/main`, left uncommitted for a session there to commit
    and publish:
    - `context/cli-applications.md`, new. It records the Elemental CLI layout that slab,
      spike-blobfs, and `clutch` share, its conventions, where the three differ, and the plan:
      decide whether `go-cli-sdk` exists, then build `go-cli-sdk-template` or
      `go-cli-template`.
    - `context/reset/ai-experiment.md`: its Disposition records, for the wave fold, the Notes
      index line and the roadmap's `goals.cli` with the tasks `sdk-decision` and `template`.
  - A brief for spike-messaging's session to adopt the layout. The architect reports that
    spike-messaging has adopted it, which bears on the note's assumption that the layout still
    fits there.
- **Validated:**
  - **Checkpoint 1** (clutch matches pidrive). `clutch scenario exchange` ended `stop=stop`.
    `scenario cancel` ended in `message_end`, `cancelled`, and `ended`, all `stop=aborted`.
    `session send` and `list` worked, and no `pi` process was left. The Adjust `ef06815`
    hoisted the shared stub harness into `internal/harnesstest`.
  - **Checkpoint 2** (live resume across processes):
    - `session send`, then `session send --session <id>` in a new Pi process, answered from the
      first exchange.
    - `session exchanges --verify` listed both exchange IDs, with Pi entry spans of 3 and 2.
    - `scenario resume` narrated the same flow.
    - Resuming from another directory failed with `ErrJournalMismatch`.
  - **Checkpoint 3** (final validation):
    - The editor pass ran on Sonnet (`fd72e47`).
    - `go build ./... && go vet ./... && go test -race ./...` passed, and golangci-lint 2.13.2
      reported 0 issues. No test process was left.
    - The live runs were repeated from a clean `--state`.
  - **Branch review** (reviewer on Opus). Findings 1–6 and the minor points were fixed in the
    Adjust `9f55599`:
    - A failed binding is still recorded, and the cursor resyncs from the journal head.
    - A cancel is refused once `EventEnded` arrives.
    - `--verify` says what it checked.
    - The broad `ErrUnknownEntry` mapping is documented.
    - The scenarios fail on an exchange error.
    - `harnesstest`'s `Close` is safe during a run.
    - The fake Pi's journal matches the capture.

    Each new test fails with its fix reverted. The suite and lint are clean, and the live
    `--verify` and `scenario resume` runs passed.

## Next-focus

Path step 3, payloads, tool calls, and skills, in this repository.

- Register a tool and a skill through Pi, and observe the tool-call events end to end.
- Interpret a structured, schema-validated response.
- Take on what `context/payloads.md` holds:
  - `Event.Err` as an `error`
  - the `Connection` reporting its exit error
  - `Usage` with cache reads and writes
- Each capability gets its own `clutch` scenario.

The design question to settle first: how a skill and a tool reach Pi. Pi runs with `-ns` and
`-ne`, so its own discovery is off. The choices are explicit flags such as `--tools`, `-e`, and
a skill path, or RPC commands. And how does a structured response get its schema: in the prompt,
through a tool, or through a Pi extension such as `structured-output.ts`?
