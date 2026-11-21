# goal · harness.adoption-spikes

- **State:** idle
- **Task:** none
- **Branch:** none

## Tasks

1. [x] model-gateway
2. [x] model-engines
3. [x] hosting-upgrades
4. [x] harness-driver

## Decisions

- model-gateway: Go lint in the spikes stays at `go vet` where no `//nolint` exists; golangci-lint
  is not added to an archived spike. (S1)
- model-gateway: each spike pins marathon 0.22.0, not the goal's 0.21.0, and its
  `context/README.md` keeps "The question", "The answer", and "The evidence" with their anchors,
  and moves its other detail unchanged to `docs/` (spike-model-gateway: `docs/capabilities.md`).
- plan (model-engines, hosting-upgrades, harness-driver): `standards-lab/context/harness-adoption.md`
  stays at this goal's sync; harness.adoption's sync deletes it, since its `architecture` task and
  v1.client.stack's webappgen task still cite it. This sync deletes only the goal file and record,
  and hands the note's deletion and its pointers to harness.adoption.
- plan: model-engines, hosting-upgrades, and harness-driver build concurrently and share one
  session-brief review; they ship in that order, then the goal syncs. A one-time exception for
  this goal to "a session advances one task".
- plan: harness-driver's findings note moves whole to `docs/findings.md`; its answer and evidence
  move into `context/README.md`; coordinator `docs/ai-strategy.md:537` is repointed at sync.
- architect: the Fable review of every adopted repository's context runs as the final stage of
  harness.adoption's `architecture` task, before the sync that closes harness.adoption.
- hosting-upgrades: a dead link in a dated record (report, measure, screen) is repointed to the
  same file at a fixed commit, the one edit those records take; rejected unlinked text (architect).
- model-engines: each engine guide holds a `scripts.md` with its `host/` libraries' reasoning
  (STANDARDS.md "Docs"); rejected spreading it over the existing guide pages.
- harness-driver: currency marks a pin carrying a hold reason `(held)` and still exits nonzero.
- plan: harness-driver holds Pi 0.99.2 and OpenCode 1.18.34, the conformance pins, until a live
  `clutch conform` run moves them; go 1.27.4 and jq 1.8.2 are pinned.

## Pending edits

- coordinator · standards-lab: `roadmap/harness.adoption-spikes.toml`: delete;
  `roadmap/index.toml`: remove harness.adoption-spikes from `active`, stage nothing, and reorder
  `planned` so it begins corpus.docs, v1.data.extensions, docproc-coverage,
  capstone.data-intelligence, sites.status-reports, sites.training.foundation, the rest in their
  current order (architect: finish the active goals first; the two sites goals run beside the
  capstone).
- coordinator · standards-lab: `docs/ai-strategy.md:537`: harness-driver's answer link points at
  `context/README.md#the-answer`.
- coordinator · standards-lab: `roadmap/harness.adoption.toml`, task `architecture`: `repos`
  stays `["architecture"]` for its core work; its summary gains two stages after the core work:
  - **Fable review**, the task's final stage before the sync: a Fable-model subagent reviews the
    context of every adopted repository, ai-hosting included, and the task's plan proposes its
    scope and batching before it runs. Each repository's context must (1) be accurate and well
    formed, lose no critical information, and keep nothing extraneous or outdated; (2) describe
    only what is currently, objectively true, or what helps a move to an anticipated future
    state, flagging for removal any log of how or why something changed that produced no
    standard or principle ruling; (3) read in a natural flow from its entry point (README,
    `context/README.md`): any branch of links builds understanding from its concepts,
    foundations before specifics, with no dead ends, forward references to undefined ideas, or
    circular detours.
  - **Sync**: the goal's sync deletes `context/harness-adoption.md` and its pointers
    (`context/README.md:104-105`, `roadmap/harness.adoption.toml:5,13`,
    `roadmap/v1.client.stack.toml:132`).
