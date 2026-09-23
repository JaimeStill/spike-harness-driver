# reset · spike-harness-driver

- **Status:** closeout
- **Session:** init
- **Branch:** main

## Disposition

- **Add or sharpen:** `context/README.md` states the question, the decision it changes, and the
  capability map.
- **Validated:** the setup, published to `github.com/JaimeStill/spike-harness-driver`.

## Next-focus

The session interface and a Pi adapter. Pi runs in `--mode rpc` against the Framework desktop's
llama.cpp router, reached through `LLAMA_BASE_URL`. The step:

- opens a session and sends two scoped exchanges in it
- streams each exchange's events, normalized and tagged with its exchange ID
- cancels one exchange mid-stream

A `cmd/` run validates it, together with tests on the event normalization. The module path is
`github.com/JaimeStill/spike-harness-driver`.
