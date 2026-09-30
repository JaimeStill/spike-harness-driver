# reset · native-capabilities

- **Status:** closeout
- **Session:** start
- **Branch:** native-capabilities

## Disposition

- **Add or sharpen:**
  - `context/findings.md`:
    - Capabilities records:
      - Pi exposes vision alone, as RPC prompt images, and drops an image quietly for a model
        without image input.
      - Pi has nothing for embeddings or audio.
      - The router's capability routes.
      - Azure v1 serves chat and embeddings, while its transcription works only on the
        deployment route.
      - Reasoning models reject `max_tokens` and `temperature`.
      - Azure Government lists no transcription model.
      - A Go tool reaches a capability the harness lacks.
      - Qwen3.8-27B's decode rate.
    - Payloads records that images travel as prompt images and that a record keeps only their
      media type.
    - Infrastructure shape records:
      - `model` as the second surface: about 610 lines, the standard library only, and one
        `Config` with no policy.
      - The token-source seam.
      - The rejected openai-go SDK and tau shape.
      - Review finding 11: `clutch`'s internal test packages.
    - Process lifecycle records:
      - The harness installed as "latest", with Pi 0.99's `-ne` and `-e builtin:llama.cpp`.
      - Pi's saved model catalog and its background refresh, which races `set_model`.
      - `pi update --models` not refreshing llama.cpp.
  - `context/service-runtime.md`, for step 6:
    - pin the harness version;
    - size or swap the capability models, with a swap landing in the harness's catalog;
    - split audio on the client;
    - managed identity through IMDS or an azidentity sub-module;
    - redact the endpoint from logged errors (review finding 7).
  - `context/adapters.md`, for step 5:
    - native capabilities per harness;
    - how each harness learns a provider's models;
    - a cloud harness target;
    - the suite's reply checks that depend on wording (review finding 6).
  - `context/README.md`: Path marks step 4 built, and names `--target` and `setup/`.
- **Retained:** `context/adapters.md` (step 5) and `context/service-runtime.md` (step 6).
- **Validated:**
  - **Checkpoint 1** (setup, from `setup/router-models.md` and `setup/azure-foundry.md`):
    - The router's Qwen3.8-27B, Qwen3-Embedding-4B, and Gemma 4 E4B answered their smoke tests:
      the shapes named, 2560 dimensions, and "The access code is 7429." both transcribed and in
      chat.
    - Azure answered keyless with the Foundry User role and the `https://ai.azure.com` scope:
      gpt-5-mini, text-embedding-3-small at 1536 dimensions, and gpt-4o-mini-transcribe on the
      deployment route.
    - The v1 transcription route answered 404.
    - The subscription has no quota for gpt-6-luna or gpt-transcribe; switching is a flag.
    - `disableLocalAuth` is on, and GitHub secret scanning and push protection are on.
    - Adjusts: `e653742`, `20c4e59`, `4c4e149`, `f1cd7ca`, `39d8342`, `79af481`, `262e7f6`,
      `a34a4a0`.
  - **Checkpoint 2** (live on the router): `vision`, `embed`, and `audio` passed.
    - `vision` first failed at `set_model`.
    - `27174d5` misdiagnosed it as a stale catalog.
    - `e1d0490` found the cause: mise had updated Pi to 0.99.1, whose `-ne` drops the built-in
      llama.cpp provider. The driver now loads it by name.
  - **Checkpoint 3** (live on Azure, `--target azure`): the three scenarios passed with no
    endpoint or token in the output.
    - `vision` failed at `set_model` again. `b788ae2` found the race with Pi's background
      catalog refresh, and reverted `27174d5`, since `pi update --models` never refreshes
      llama.cpp.
    - A model loaded after Pi saved its catalog was then selected.
  - **Checkpoint 4** (final validation):
    - The editor pass ran on Sonnet (`a2d844a`).
    - Build, vet, `go test -race`, golangci-lint 2.13.2, and gofmt were clean in all three
      modules.
    - All nine scenarios passed live on Pi 0.99.1 from a clean `--state`, with the example
      tools and skills, and the three capability scenarios passed on Azure.
  - **Branch review** (reviewer on Opus). Findings 1–5 and 8–10 were fixed in Adjust `7e0c59a`:
    - `set_model` asks again only on Pi's "Model not found" for llama.cpp. A Pi that has exited
      fails at once.
    - A record keeps no image bytes.
    - The wait is `Driver.CatalogWait`.
    - `hasCode` ends a run at punctuation.
    - A test covers vision steps 2 and 3.
    - `fakePi`'s doc comment is back on `fakePi`.
    - The runbook passes the token off curl's arguments.
    - The target table is tidied.

    Checks passed in all three modules, and the race was re-proved live with Gemma. Findings 6,
    7, and 11 are recorded in the notes above. Finding 12, that `clutch` builds only in the
    workspace, predates the branch and was already recorded.

## Next-focus

Path step 5, the Claude Code and OpenCode adapters, in this repository. `context/adapters.md`
holds the plan and its open questions.

- Each adapter is a `harness.Connection` over `harness/stdio`:
  - Claude Code: `claude -p` with `stream-json` and control requests.
  - OpenCode: `opencode acp`.
- A conformance suite runs Pi and both new adapters behind one interface, against the router and
  a cloud target.
- Settle first, per harness:
  - persistence: a durable journal with stable entry IDs, which decides whether
    `harness.Journal` becomes required or is dropped;
  - how Go tools reach it, and whether one Go MCP server serves every harness;
  - whether it takes images;
  - how it learns a provider's models.
- Pin each harness's version for the suite's runs. Pi changed under this step.
- The review's surface change waits for the second adapter: fold `respond` into
  `EventStructured`, with a validation-failure event, and sum `Usage` over the exchange.
