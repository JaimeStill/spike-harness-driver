# goal · harness.adoption-spikes

- **State:** building
- **Task:** model-gateway
- **Branch:** model-gateway

## Tasks

1. [ ] model-gateway
2. [ ] model-engines
3. [ ] hosting-upgrades
4. [ ] harness-driver

## Task brief · model-gateway

**Problem**

spike-model-gateway predates marathon 0.22.0's standards: no pin, a check that runs neither lint,
a context README twice its budget with no capability map, about 1,600 comment lines with
reasoning outside the five kinds, pointers to a roadmap and an experiment section that no longer
exist, and four trailing pins. It stays live until v1.ai.hosting's sync archives it.

**Behaviors**
1. `retry/` requires go-ai v0.4.0; the client helpers use `tools/infer/v0.3.1`; PostgreSQL is
   19.0-alpine. Currency reports only the held Python.
2. `mise.toml` pins marathon 0.22.0 (`version_prefix = "marathon/v"`) under a `# See` header;
   currency reports a newer marathon tag.
3. The check keeps its steps and also runs lint-context and lint-references, failing if either
   fails or is missing from PATH (`FAIL: <lint> is not on PATH; run mise install`); its header and
   task description name what it runs.
4. lint-context passes: `context/README.md` is at most 1,000 words and keeps "The question", "The
   answer" (the pointer to the root README), and "The evidence" with its anchor, each evidence item
   linking its finding page; it has a `## Capability map` of `(built)` entries linking their homes;
   the Capabilities, Path, and References detail moves unchanged to docs/; Rules become a pointer
   to the coordinator's experiment rules.
5. Every comment in scripts, Go tests, YAML, TOML, compose, Dockerfiles, and `.gitignore` is one of
   the five kinds; removed reasoning moves to docs/, a README, a task description, or a name;
   section comments become named functions with behavior unchanged; test-function doc comments go;
   lint-references passes.
6. `STANDARDS.md` states the comment rule for scripts and every configuration file.
7. Every roadmap pointer names `standards-lab/roadmap/index.toml`; no pointer names a deleted goal
   record or section.
8. No existing docs/ page moves or is renamed, no dated finding changes, and inbound links keep
   working.

**Test seams**
- `mise run check`, `mise run currency`, lint-context, lint-references; each configuration file with
  comments stripped is identical before and after; each sourced script library's `declare -F`
  lists every function it listed before.

**Slices**
1. Upgrade: go-ai v0.4.0, infer v0.3.1, PostgreSQL 19.0-alpine; Python held.
2. Configuration comments and the STANDARDS rule.
3. Script and Go-test comments.
4. Context and pointers.
5. Pin and check.

**Out of scope**

Redeploying the stack; re-measuring or changing any answer or finding; moving docs/ pages; CI;
golangci-lint; coordinator and ai-hosting edits; the Python hold; archiving.

**Door**

Two-way: no release; the spike repository only.

## Progress

slices 0/5 committed · standards — · spec — · editor —

## Decisions

- model-gateway: Go lint in the spikes stays at `go vet` where no `//nolint` exists; golangci-lint
  is not added to an archived spike. (S1)

## Pending edits

None.
