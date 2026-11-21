# goal · harness.adoption-spikes

- **State:** brief ready
- **Task:** model-engines, hosting-upgrades, harness-driver
- **Branch:** model-engines, hosting-upgrades, harness-driver

## Tasks

1. [x] model-gateway
2. [ ] model-engines
3. [ ] hosting-upgrades
4. [ ] harness-driver

## Task brief · harness.adoption-spikes · model-engines

**Problem**

spike-model-engines predates marathon 0.22.0's standards: no pin, a check that runs only
shellcheck, a context README three times its budget with no capability map and no "The answer",
about 770 comment and header lines outside the five kinds, 14 cross-spike references lint-references
rejects, pointers to a roadmap, goal record, and experiment section that no longer exist, and a
trailing llama.cpp pin. Its `host/` suite serves vLLM rows until v1.ai.hosting's sync archives it.

**Behaviors**
1. Both host classes' llama.cpp pins name the newest b-build with all three flavors' server images
   (b12602 at planning); guides and context naming the current pin name it; `LLAMA_RELEASE` stays
   v0.8.0. Currency reports only the held bench and native Python.
2. `mise.toml` pins marathon 0.22.0 (`version_prefix = "marathon/v"`) under a `# See` header;
   currency reports a newer marathon tag.
3. The check keeps shellcheck and also runs lint-context and lint-references, failing if either
   fails or is missing from PATH (`FAIL: <lint> is not on PATH; run mise install`); its header,
   task description, and CLAUDE.md name what it runs.
4. lint-context passes: `context/README.md` ≤ 1,000 words; keeps "The question" and "The evidence"
   with anchors, each evidence item linking its finding pages; gains "The answer" (pointer to the
   root README plus the vLLM-superseded line); has a `## Capability map` of `(built)` entries; the
   Capabilities, Path, References, and host-practice detail moves unchanged to docs/; "## Rules"
   keeps its heading and anchor, pointing to the coordinator's experiment rules and a docs/ page
   with the spike-specific rules.
5. Every comment in scripts, `host/` commands and libraries, YAML, TOML, `.env`, the rows file, the
   INI preset, systemd units, sudoers, the Dockerfile, `.gitignore`, and `.shellcheckrc` is one of
   the five kinds; reasoning moves to docs/, a guide, a task description, or a name; section
   comments become named functions with behavior unchanged; `# engines:summary=` lines and
   `# shellcheck source=` directives are unchanged; cross-spike references name their sibling repo;
   prose saying a file's comments hold its reasoning names the new home; lint-references passes.
6. `STANDARDS.md` states the comment rule for scripts and every configuration kind above.
7. Every roadmap pointer names `standards-lab/roadmap/index.toml`; every rules pointer names
   `standards-lab/docs/ai-hosting.md#experiment-rules`; none names a deleted record or section.
8. No docs/ page moves or renames; no dated finding changes; inbound links keep working
   (`README#the-answer`, `context/README.md#rules`, `#the-evidence`, `host/`, `docs/engines/`,
   `docs/findings/` paths).

**Test seams**
- `mise run check`, `mise run currency`, lint-context, lint-references; each configuration file
  with comments stripped identical before and after; each sourced library's `declare -F` lists
  every function it listed before; `host/engines`' listing identical; `vllm_row_words` per row identical.

**Slices**
1. Upgrade: llama.cpp pin on both host classes and the guides naming it; Python held.
2. Configuration comments, cross-spike prefixes, and the STANDARDS rule.
3. Script comments.
4. Context and pointers.
5. Pin and check.

**Out of scope**

Running host commands, redeploying or updating rows; re-measuring or changing any answer or
finding; moving docs/ pages; the audio Dockerfile's pip pins; CI; coordinator and ai-hosting edits;
the Python holds; archiving.

**Door**

Two-way: no release; the spike repository only.

## Task brief · harness.adoption-spikes · hosting-upgrades

**Problem**

spike-hosting-upgrades predates marathon 0.22.0's standards: its check fails on main on four links
to the deleted goal record, neither lint runs, its context README is half again over budget with no
capability map or answer pointer, about 460 comment lines fall outside the five kinds, it points at
a roadmap and experiment section that no longer exist, and uv trails. It stays live until
v1.ai.hosting's sync archives it.

**Behaviors**
1. The bench environment pins uv 0.13.9; currency reports only the held Python.
2. The four links to the deleted goal record become permalinks to the same file at 7a7bfeb; the
   check passes again; no other text in a dated record changes; STANDARDS' never-edited rule names
   that exception.
3. `mise.toml` pins marathon 0.22.0 (`version_prefix = "marathon/v"`) under a `# See` header;
   currency reports a newer marathon tag.
4. The check keeps its shellcheck and link steps and also runs lint-context and lint-references,
   failing if either fails or is missing from PATH (`FAIL: <lint> is not on PATH; run mise
   install`); its header and task description name what it runs.
5. lint-context passes: `context/README.md` ≤ 1,000 words; keeps "The question"; adds "The answer"
   pointing to the root README; "The evidence" keeps its anchor, each item linking the records that
   prove it; a `## Capability map` of `(built)` entries; Capabilities, Path, References, and the
   spike's own rules move unchanged to a docs/ page; Rules points to the coordinator's experiment rules.
6. Every comment in scripts, the skill's scripts, YAML, TOML, and `.gitignore` is one of the five
   kinds; skill-script reasoning moves to the skill's mode files (`See` lines) or one new reference
   page SKILL.md doesn't load; other reasoning moves to docs/ or a name; section comments become
   named functions with behavior unchanged; each shellcheck suppression carries its reason;
   cross-spike references name their sibling repo and skill references give their path from the
   root; lint-references passes.
7. `STANDARDS.md` states the comment rule for scripts and every configuration file.
8. Every roadmap pointer names `standards-lab/roadmap/index.toml`; every rules pointer names
   `standards-lab/docs/ai-hosting.md#experiment-rules`; none names the deleted record or section.
9. No docs/ page moves or renames; no finding or record changes beyond behavior 2; the root
   README's "The answer" and `docs/convention.md` keep their URLs.

**Test seams**
- `mise run check`, `mise run currency`, lint-context, lint-references; stripped configs identical;
  `declare -F` on the measure helpers lists every function it listed before; probe.sh's read-only
  output on framework and gb10-1, before and after, identical apart from readings that vary per run.

**Slices**
1. Upgrade uv; repair the four dead links (the check passes).
2. Configuration comments, sibling prefixes, and the STANDARDS rule.
3. Script and skill-script comments.
4. Context and pointers.
5. Pin and check.

**Out of scope**

Changing the skill's procedure beyond pointers and paths; re-surveying or re-measuring; changing
any answer or finding; moving docs/ pages; CI; coordinator and ai-hosting edits; the Python hold;
archiving.

**Door**

Two-way: no release; the spike repository only. The probe runs are read-only.

## Task brief · harness.adoption-spikes · harness-driver

**Problem**

spike-harness-driver, the goal's root, predates marathon 0.22.0's standards: no pin, no currency
command, no STANDARDS.md, a check that runs neither lint and calls unpinned go and jq, a findings
note four times its budget holding the answer the coordinator links, an unmarked capability map,
about 1,900 Go comment lines outside the five kinds, 8 unresolved references, and a pointer to a
roadmap goal that no longer exists. It stays live until experiment.harness's intake; spike-go-harness
runs its conform before then.

**Behaviors**
1. clutch requires pflag v1.1.0; node is 26.13.1; Pi 0.99.2 and OpenCode 1.18.34 stay at their
   conformance pins, each with its reason beside the pin.
2. `mise.toml` pins marathon 0.22.0 (`version_prefix = "marathon/v"`), go 1.27.4, and jq 1.8.2,
   with `task.run_auto_install = false`, under a `# See` header.
3. A currency command reports every mise tool and every go.work module's direct requirement behind
   its latest, a newer marathon tag included; it exits nonzero when any trail and reports held pins
   as held.
4. The check keeps its steps and also runs lint-context and lint-references, failing if either
   fails or is missing from PATH (`FAIL: <lint> is not on PATH; run mise install`); its header and
   task description name what it runs.
5. lint-context passes: `context/README.md` ≤ 1,000 words with "The question", "The answer" (moved
   from the findings note, citations as links), and "The evidence" (numbered, agreeing with the
   coordinator's seven, each linking its finding), each with its anchor, and a `## Capability map`
   of `(built)` entries; the findings note's other content moves whole and unchanged to
   `docs/findings.md`.
6. Every Go comment is package docs, an exported identifier's doc, or one of the other four kinds;
   doc comments on unexported identifiers and test functions go; reasoning moves to exported or
   package docs, names, or docs/, never lost; the exported API is unchanged.
7. Every comment in TypeScript, scripts, TOML, `.gitignore`, `.dockerignore`, and the Containerfile
   is one of the five kinds; configuration headers are a short description plus `See` lines;
   lint-references passes.
8. `STANDARDS.md` exists, stating the comment rule for Go, TypeScript, scripts, and every
   configuration file, and the repo's judgement calls.
9. Every roadmap pointer names `standards-lab/roadmap/index.toml`; every pointer to the findings
   note names its new page or the answer's new home.
10. No dated finding or answer changes; inbound links work or have a pending edit.

**Test seams**
- `mise run check` (race tests), `mise run currency`, lint-context, lint-references; `go doc -all`
  per package identical except added reasoning; stripped configs identical.

**Slices**
1. Upgrade: pflag v1.1.0, node 26.13.1; Pi and OpenCode held with reasons.
2. Configuration and TypeScript comments, and STANDARDS.md.
3. Go comments: root module and mcpbridge.
4. Go comments: pi, claude, opencode adapters.
5. Go comments: clutch.
6. Context and pointers: findings → `docs/findings.md`, answer and evidence into `context/README.md`, map, pointers.
7. Pin, check, and currency.

**Out of scope**

Re-running conform or re-measuring; changing any answer or finding; golangci-lint (S1); CI; a root
README; the `go` directive; Claude Code's version; go-ai and ai-hosting edits; deleting
harness-adoption.md; archiving.

**Door**

Two-way: no release; the spike repository only; the sync's coordinator edits follow on accept.

**Sync (for the session brief)**
- coordinator: `docs/ai-strategy.md:537` → `…/spike-harness-driver/blob/main/context/README.md#the-answer`;
  `roadmap/index.toml` drops harness.adoption-spikes from `active`; `roadmap/harness.adoption-spikes.toml` deleted.
- claude-plugins (harness.adoption's record, locked by that goal → lands as a task/pending edit of
  it): own harness-adoption.md's deletion and its pointers.
- root: record deleted after the sync PR merges.
- next: sites.status-reports (top of `planned`), staged on accept if staging's checks pass.

## Progress

- model-engines: slices 5/5 committed · standards ✓ · spec ✓ · editor ✓
- hosting-upgrades: slices 5/5 committed · standards ✓ · spec ✓ · editor ✓
- harness-driver: slices 7/7 committed · standards ✓ · spec ✓ (1 gap closed) · editor ✓
- Architect: the three tasks build concurrently, each repository's slices in order; the record
  stays on the root's `model-engines` branch (worktree) until each ships.

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
