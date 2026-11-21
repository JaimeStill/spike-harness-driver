# goal · harness.adoption-spikes

- **State:** building
- **Task:** model-engines
- **Branch:** model-engines

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

## Progress

slices 0/5 committed

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
- plan: model-engines and hosting-upgrades share one session-brief review; model-engines waits at
  `brief ready` on its branch while hosting-upgrades builds, a one-time exception for this goal.
- plan: harness-driver's findings note moves whole to `docs/findings.md`; its answer and evidence
  move into `context/README.md`; coordinator `docs/ai-strategy.md:537` is repointed at sync.
- plan: harness-driver holds Pi 0.99.2 and OpenCode 1.18.34, the conformance pins, until a live
  `clutch conform` run moves them; go 1.27.4 and jq 1.8.2 are pinned.

## Pending edits

- coordinator · standards-lab: `roadmap/harness.adoption-spikes.toml`: delete;
  `roadmap/index.toml`: remove harness.adoption-spikes from `active`.
- coordinator · standards-lab: `roadmap/harness.adoption.toml`, task `architecture`'s summary:
  the goal's sync deletes `context/harness-adoption.md` and its pointers
  (`context/README.md:104-105`, `roadmap/harness.adoption.toml:5,13`,
  `roadmap/v1.client.stack.toml:132`).
