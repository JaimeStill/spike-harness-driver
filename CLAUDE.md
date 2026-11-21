# spike-harness-driver

A spike that tests whether Go can drive an external agent harness as the infrastructure for
agentic work. It is a `code` project run with the marathon workflow. Start from
`context/README.md`. `STANDARDS.md` lists the judgement calls the check can't enforce.

- **Check:** `mise run check` builds, vets, format-checks, and race-tests every `go.work` module,
  tests the Pi bridge, and runs lint-context and lint-references.
- **Currency:** `mise run currency` reports every `mise.toml` and `go.mod` pin behind its
  latest.
