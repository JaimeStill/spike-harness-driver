# reset · session-interface-pi-adapter

- **Status:** closeout
- **Session:** start
- **Branch:** session-interface-pi-adapter

## Disposition

- **Add or sharpen:**
  - `context/findings.md` records what step 1 showed. It covers exchange scoping without event
    IDs, Pi's cancellation order, the three-layer shape (provisional until step 5), and the
    process lifecycle.
  - `context/payloads.md`, `context/adapters.md`, and `context/service-runtime.md` hold what
    steps 3, 5, and 6 inherit. Those are review findings m1 and m3, the `Event.Err` question,
    the adapter routes, and process cleanup with its rejected alternatives.
  - `context/README.md` drops step 1 from Path and lists `opencode acp` as a route for OpenCode.
- **Validated:**
  - **Checkpoint 1.** `go run ./cmd/pidrive` showed two exchanges under distinct IDs, the
    second cancelled mid-stream: `message_end`, `cancelled`, and `ended`, all `stop=aborted`.
    The architect re-planned from here into three layers: `harness`, `harness/stdio`, and `pi`.
  - **Checkpoint 2.** The same run on the layered build, plus these confirmed adjustments:
    - `Connection` naming
    - `Client.read` split into `dispatch` and `shutdown`, with `EventQueue` and `calls`
      extracted
    - lifetime contexts for the process and the session
    - process-group cleanup, with tests that assert no orphan survives
  - **Branch review.** Findings 1–8 were fixed and cited in the review-adjustment commits, and
    each fix has a test that fails without it. Finally:
    - `go build ./... && go vet ./... && go test -race ./...` pass, and golangci-lint 2.13.2
      reports 0 issues.
    - No test process is left over.
    - The pidrive run showed exchange 1 ending `stop=stop` and exchange 2 ending
      `stop=aborted`, with no close error and no `pi` process left behind.

## Next-focus

Path step 2, persistence and resume, in this repository.

- Drop `--no-session` and resume a Pi session by ID, with `--session-id` or `switch_session`.
- Show that the session outlives one Pi process and that its exchange IDs survive the resume.

The design question to settle first: the driver assigns exchange IDs in memory, so it either
records them itself or maps them onto Pi's session entries (`get_entries`, `entry_appended`).
