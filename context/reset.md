# reset · long-running-workflow

- **Status:** closeout
- **Session:** start
- **Branch:** long-running-workflow

## Disposition

The spike is complete: step 6, the last on its Path, is built and validated, and its validation
answers the question.

**The question.** Can Go drive an external agent harness (Pi, Claude Code, or OpenCode, against
local and cloud models) as the infrastructure for agentic work, and what does that
infrastructure need to be? The answer settles go-ai's shape and whether a service's container
image carries a harness, for the goal `experiment.ai` (formerly `v1.ai.experiment`).

**The answer. Yes.** `context/findings.md` holds the evidence by part. "The answer" there is
the summary, and "Open for a service" lists what an adopting service still owes. By part:

- **Capabilities.**
  - Answer: tools and skills run through every harness; of the native capabilities, harnesses
    expose vision alone, and embeddings and audio need a direct model client.
  - Evidence (steps 3–5):
    - A Go tool reached Pi through its bridge extension, and reached Claude Code and OpenCode
      through `mcpbridge`.
    - Skills loaded on all three harnesses.
    - Every harness dropped an image a model couldn't take, quietly.
    - `model`, one OpenAI-compatible client, served vision, embeddings, and audio on the router
      and Azure.
    - A Go tool that calls `model` gave a Pi session a transcript.
    - `clutch conform` passed every capability on Azure and Anthropic for all three harnesses.
- **Sessions.**
  - Answer: a session outlives its process on every harness and resumes by ID with its history.
    The driver's exchange IDs survive through a `harness.Store`, bound to Pi's journal.
  - Evidence (steps 2 and 5):
    - A follow-up in a new process answered from the earlier exchange.
    - Claude Code and OpenCode resumed from any directory.
    - `ErrJournalMismatch` caught Pi's cwd-scoped fresh session.
    - This step: workflow sessions reopened under their recorded IDs after an interrupt,
      `kill -9`, and `docker stop`, and passed the journal check.
- **Scoped exchanges.**
  - Answer: no harness tags events with a request, so a session carries one exchange at a time,
    scoped from start to settle, under a driver-assigned UUIDv7. Each harness's cancellation
    maps to one outcome, and a structured response is a `respond` tool on every harness.
  - Evidence (steps 1, 3, and 5): the conformance matrix's `exchange`, `cancel`, `structured`,
    and `resume` capabilities.
- **Long-running workflows** (this step).
  - Answer: a standard-library layer of about 1,800 lines over `harness.Driver` is enough. A DAG
    of exchanges over named sessions, each on its own harness, runs under a concurrency limit,
    with cancellation, progress, and SSE all from one log per run, and resumes after a restart.
  - Evidence: checkpoints 1–4 below.
- **For go-ai:** both surfaces, harness sessions and a model client, with the workflow layer
  above `harness`.
- **For deployment:** a service image carries one pinned harness, Pi by default. The image is
  215 MB, idles at about 10 MiB, and adds about 95 MiB per Pi session.

Note operations:

- **Integrated:** deleted `context/service-runtime.md`.
  - What step 6 built is now in the code and its documentation: `tini` as the orphan backstop
    (`deploy/`), the pinned and checksummed harness, the usage-limit pause (`workflow`), and a
    private state volume.
  - Process cleanup's rejected alternatives moved to `findings.md`, Process lifecycle.
  - Every open item moved to `findings.md`, Open for a service, so findings is the one document
    the coordinator reads.
- **Add or sharpen:**
  - `context/findings.md`:
    - New parts: **Workflows**, **Deployment**, **Managed identity and IL6, on paper**,
      **Open for a service**, and **The answer**.
    - **Workflows** covers:
      - the DAG model, one log per run, and the limit's ordered slots;
      - cancel versus interrupt;
      - resume with adoption, and settling a decided outcome;
      - the usage-limit pause, and one state directory per process;
      - a resumed step that took 5 minutes;
      - tau `orchestrate`, herald's SSE, and `claude-classify-docs` as rejected prior art.
    - **Deployment** covers:
      - the image's sizes and layers;
      - why Debian and not Alpine (Pi ships glibc only), with distroless as the smaller route;
      - `tini`;
      - the `strace` probe: Pi makes no outbound call at start.
    - **Managed identity and IL6** covers:
      - `clutch token` over IMDS as Pi's key command, since the image has no `az`;
      - OpenCode's fixed token;
      - no transcription on Azure Government;
      - allowlisted endpoints;
      - client authentication, a gap the service fills.
    - Infrastructure shape: the design held for the workflow, which is no longer provisional,
      and the core now includes the workflow layer.
    - Process lifecycle: Pi exits when its driver dies, and `tini` reaps what a harness started.
  - `context/README.md`: Path marks every step built, including step 6, and names the
    `workflow/...` layer, `clutch workflow`, `clutch serve`, and `deploy/`.
- **Retained:** none. `context/` holds `README.md`, `findings.md`, and this file.
- **Validated:** all four checkpoints were confirmed by the architect, each run live with Pi
  0.99.2 on gpt-oss over the llama.cpp router unless noted.
  - **Checkpoint 1** (stages 1–4: the `workflow` model, the Runner, `workflow/filestore`, and
    `clutch workflow` with `examples/workflows/review.json`):
    - A five-step run over four sessions with `--limit 2` ended `done` in 34 s. The lead
      session's second step answered from its first.
    - SIGINT during `decide` left the run unfinished. `workflow resume`, run from another
      directory, reopened the lead session under its recorded ID, ran only the two unfinished
      steps, and ended `done`.
    - Follow-up `3689ef3` fixed a concern the session raised at the checkpoint: steps now take
      their slot before launch, in declaration order.
  - **Checkpoint 2** (stages 5–6: `workflow/sse` and `clutch serve`):
    - SSE delivered 16 logged events with ids and 1,259 live events.
    - Reconnecting with `Last-Event-ID: 13` replayed exactly 14–16.
    - `DELETE` on a live run answered 202, ended it `cancelled`, and left no Pi process.
    - `kill -9` of the server mid-run: both Pi children exited as their stdin closed. The
      restarted server, started from `/tmp`, resumed the run, which ended `done` at seq 19.
    - SIGTERM closed the open streams and left the run for the next start.
  - **Checkpoint 3** (stage 7: `deploy/`):
    - The standalone image is 215 MB, and the npm-on-Node image 377 MB.
    - In the container, the example ran `done` in 33 s over SSE.
    - `docker stop` went through `tini` and stopped cleanly, and `docker start` resumed the run
      to `done`.
  - **Checkpoint 4** (stage 8: editor pass `af2bf4e`, on Sonnet):
    - Build, vet, `go test -race`, golangci-lint 2.13.2, and gofmt were clean in all six
      modules.
    - A mixed run, Pi for the reviewers and Claude Code 2.1.287 on haiku for the lead, ended
      `done` in 39 s.
    - `strace` showed Pi making no outbound connection at start.
  - **Branch review** (reviewer on Opus): 16 findings, all verified by the session.
    - The architect chose to fix all of them, in Adjust `97b8aae`, with new tests. The most
      severe was finding 1, a resumed run whose log already recorded a failure hanging forever.
    - Checks were clean in all six modules.
    - Live in the container, a run survived `docker stop -t 35` and a restart, and resumed to
      `done`. A caught-up stream of an ended run answered 204, `GET /runs` reported no errors,
      and an invalid run ID answered 404.
    - On one earlier repeat, the two resumed reviewers took about 5 minutes each but ended
      validly. The cause is unconfirmed, and `findings.md` records it.

## Next-focus

Spike complete. The workspace carries this closeout into the experiment.ai goal record (rooted
here) under marathon 0.16, then deletes this reset file. The next spikes and the intake are
planned from the coordinator.
