# goal · harness.adoption-spikes

- **State:** idle
- **Task:** none
- **Branch:** none

## Tasks

1. [x] model-gateway
2. [ ] model-engines
3. [ ] hosting-upgrades
4. [ ] harness-driver

## Decisions

- model-gateway: Go lint in the spikes stays at `go vet` where no `//nolint` exists; golangci-lint
  is not added to an archived spike. (S1)
- model-gateway: each spike pins marathon 0.22.0, not the goal's 0.21.0, and its
  `context/README.md` keeps "The question", "The answer", and "The evidence" with their anchors,
  and moves its other detail unchanged to `docs/` (spike-model-gateway: `docs/capabilities.md`).

## Pending edits

None.
