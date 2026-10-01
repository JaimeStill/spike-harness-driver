# Findings

Evidence toward the spike's question, by part. The coordinator's `plan` session reads it with
the result. The package documentation in `harness`, `harness/stdio`, `harness/filestore`,
`harness/catalog`, `harness/cache`, `model`, `mcpbridge`, and the adapters `pi`, `claude`, and
`opencode` explains how the built code works; this note holds what the code shows about
harnesses in general. The last part compares the three harnesses.

## Exchanges

- Pi's RPC events carry no request ID. Only command responses echo the client's `id`. An
  exchange is therefore the window from `agent_start` to `agent_settled`, and a session carries
  one exchange at a time. `agent_end` doesn't end an exchange, because a retry, a compaction, or
  a queued message can start another run after it.
- A `prompt` response means only that Pi accepted the prompt. The run's outcome, including any
  error, arrives as events.
- Pi answers `abort` after the run settles. Pi keeps queued messages through an abort, so a
  cancellation sends `clear_queue` first. Pi cancels in two round trips, and the exchange can end
  between them, so a cancellation in flight holds off the next exchange.
- A cancellation can land where Pi sends no aborted message, such as during a tool call. The
  session therefore records the cancellation itself, rather than trusting the harness to report
  it.
- The driver assigns exchange IDs itself (UUIDv7), because the harness has none to offer.
- No harness tags its events with a request ID, so every adapter carries one exchange at a time:
  - Claude Code's stream-json turn runs from the system `init` message to the `result`. The
    `result` carries the turn's usage summed over its model requests; the usage on each
    assistant message is partial.
  - OpenCode answers ACP's `session/prompt` only as the turn ends, after every
    `session/update` of the turn. The adapter writes the prompt before `Prompt` returns, so a
    cancel can't overtake it, and ends the exchange when it decodes the answer, in stream order.
- Each harness cancels in its own way, and each ends the run with its own word, which the
  adapters map to "aborted":
  - Claude Code answers an `interrupt` control request at once and ends the turn with a
    terminal reason starting `aborted_`.
  - OpenCode takes `session/cancel` as a notification and answers the prompt with
    `cancelled`. It ignores a cancel for an idle session.
- Claude Code exits with status 1 at the end of its input when its last turn failed, an
  interrupted turn included, which is no error of the exit.

## Sessions

- Pi persists a session by default. `--session-id` opens a session by ID in a new process, or
  creates it, and the resumed session keeps its history: a follow-up in the new process answers
  from the earlier exchange.
- Pi scopes a session ID to the working directory, even with `--session-dir`. Resuming from
  another directory silently starts a fresh session under the same ID.
- Pi's `get_entries` gives each session entry a stable ID that survives the process, and
  `since` works as a cursor. It fails when `since` names no entry.
- Opening a session appends entries of its own. Setting the model appends three entries on a new
  session (`model_change`, `thinking_level_change`, `model_change`) and one on a resumed session.
  A session's first run appends a system message before the user message.
- A session's history names the files the harness read, such as a skill's `SKILL.md`, so those
  files must outlive the process. A skill on disk is loaded where it lives, and anything else
  the driver loads from files sits in a cache under a name its content decides. A session
  resumed in a new process re-read its skill at the path in its history.
- Exchange IDs stay the driver's own, because Pi has none to offer. They survive a resume
  through a `Store` of records, and each record is bound to the journal entries its exchange
  appended. The binding is what makes a lost or wrong session detectable:
  - On resume, the session checks that the journal still holds the last recorded entry. A
    missing entry is `ErrJournalMismatch`, which is how the cwd-scoped fresh session surfaces.
  - The check covers only the last entry.
  - The check isn't read-only, because opening Pi appends entries.
- Claude Code and OpenCode both resume a session by ID from any working directory, and fail
  loudly on an ID they don't hold. Pi's cwd hazard is Pi's alone.
  - OpenCode can't create a session under an ID the caller chooses, so its adapter refuses an
    unknown one.
  - Claude Code creates a session under a new ID, so a driver holding records for an ID whose
    transcript is gone would start fresh silently. The adapter refuses that case with
    `ErrJournalMismatch`.
  - OpenCode's `session/load` replays the history as updates, which belong to no exchange.
- **`harness.Journal` stays optional.** Neither new harness offers a read of its entries over
  its protocol, and neither has the hazard the journal guards against. It is Pi's guard, not a
  contract every harness meets.
- Rejected ways to carry exchange IDs:
  - **A Pi extension** that writes the ID into Pi's own session with `pi.appendEntry`. It works
    for Pi alone. The driver now loads an extension of its own (Payloads), so the extension
    itself is no longer the cost; the approach stays rejected because no other harness has an
    equivalent.
  - **Pi's entry ID as the exchange ID.** The ID would be known only after the prompt lands, and
    would be specific to the harness.
  - **Driver records with no binding to the harness.** The records could drift from what the
    harness holds, with nothing to detect it.
  - **`switch_session`**, which loads a session file by path inside a running Pi. It suits a
    process-per-session driver less well than `--session-id` given at start.

## Payloads

- Pi's RPC registers no tools and loads no skills. A tool reaches Pi only through an extension,
  and a skill only through `--skill`. Explicit `-e` and `--skill` paths still load with discovery
  off (`-ne`, `-ns`), so the driver keeps the user's own extensions and skills out while loading
  its own.
- The driver's baseline for Pi is one extension, the bridge. It registers the session's tools
  and asks the driver to run each call through an input dialog (`extension_ui_request`), which
  the driver answers on the same RPC stream. Tools defined in Go, including command tools in any
  language, ran end to end this way, with no second transport.
- Pi lists skills to the model only when its `read` or `bash` tool is active; the model then
  reads `SKILL.md` itself. A prompt of `/skill:<name>` inlines the skill with no tool at all.
- Pi has no provider-level response schema. A structured response is the arguments of a
  terminating tool, `respond`, which Pi validates against the exchange's schema. Registering
  `respond` again per exchange gives each exchange on one session its own shape, and
  `terminate: true` skips the follow-up model turn.
- Pi tells the model about a tool added partway through a session only as a `toolsAdded` delta
  with its name and schema, without its prompt guidelines. With another tool active, gpt-oss
  then answered in text instead of calling `respond`. An instruction appended to the prompt made
  3 of 3 runs, and every later run, call it.
- With a prompt that contradicted that instruction ("plain text only, without calling any
  tool"), gpt-oss on llama.cpp reasoned that the system instruction wins and called `respond`,
  but in about half the runs llama.cpp rejected the call ("The model produced output that does
  not match the expected peg-native format"). Pi records that as `stopReason: "error"` and
  doesn't retry it.
- The validation-retry path, a `respond` call Pi rejects and the model retries, hasn't been seen
  live: gpt-oss met every schema on its first call, including a regex pattern and exact item
  counts.
- A warm prompt cache takes most of the input: one exchange reported `input` 37 and `cacheRead`
  745. `Usage` counts both.
- An image reaches Pi as a prompt image, `{type: "image", data, mimeType}`. The harness's own
  session holds it, so an exchange record keeps only its media type, never its bytes.
- **Claude Code and OpenCode reach a program's tools only as an MCP server's**, so the driver
  serves the session's tools from `mcpbridge`:
  - Claude Code takes an "sdk" MCP server whose JSON-RPC travels inside its own control
    protocol, `mcp_message` requests the driver answers. No second transport is needed.
  - OpenCode takes the servers ACP's `session/new` names, so the bridge listens on loopback
    HTTP behind a per-session bearer token.
  - Pi keeps its bridge extension. One Go MCP server for all three was possible, but Pi's
    in-process route needed nothing more.
- **A structured response is a `respond` tool on every harness**, validated in Go by the
  bridge for the new two:
  - Claude Code caches a tool's input schema by its name, so after a change under the same
    name the model still saw the old schema and refused the call. The tool's name therefore
    carries a digest of its schema, `respond_<8 hex>`.
  - A harness learns of a changed tool list by notification and lists the tools again in its
    own time. A prompt that overtakes the listing reaches a model that doesn't see the new
    tool, which happened on both harnesses, so a prompt waits for a listing that started after
    the change.
  - Claude Code's own `--json-schema` is rejected: it is fixed for the whole process, while each
    exchange carries its own schema. ACP has no response schema.
  - Each codec accepts a response only when the bridge's own acceptance comes back, so the
    verdict doesn't rest on how a harness marks a failed MCP call.
- **Skills:**
  - Claude Code loads them from a plugin directory (`--plugin-dir`), named
    `<plugin>:<skill>`, through its `Skill` tool. `/driver:<skill>` inlines one.
  - OpenCode loads them from the `skills.paths` key of its configuration, through its `skill`
    tool. `/<skill>` inlines one.
- **The usage limit of Claude Code's subscription** arrives as `rate_limit_event` notices,
  which become `EventLimit`. A turn the limit refused becomes a `*harness.LimitError` with its
  reset time. The refused path is tested only on documented shapes, since the limit can't be
  reached on demand.

## Capabilities

- gpt-oss-120b on the llama.cpp router streams thinking deltas, although Pi's model catalog marks
  it `reasoning: false`.
- The router loads models on demand and lists some as unloaded. An exchange against a cold model
  can take minutes to produce its first token (`service-runtime.md`).
- **Pi exposes vision and nothing else native.**
  - Its RPC `prompt`, `steer`, and `follow_up` take images.
  - Pi decides whether a model takes images from the provider's catalog, which for the router
    is `/models`' `input_modalities`.
  - For a model without image input, Pi replaces the image with "(image omitted: model does not
    support images)" and the exchange still succeeds. The model then says it can't see an
    image, and no error tells the driver the image was dropped.
  - Pi has no content type or command for embeddings or audio, and its model configuration
    rejects an `audio` input. They need a direct model client.
- **A direct client covers all three with one OpenAI-compatible shape.**
  - The router serves `/v1/chat/completions` with image and `input_audio` parts,
    `/v1/embeddings`, and `/v1/audio/transcriptions`. It has no text-to-speech route.
  - Azure AI Foundry's v1 API serves chat and embeddings at one base URL. Its v1 transcription
    route answers 404 `DeploymentNotFound` for a deployment that exists; the older deployment
    route with an `api-version` works. So an endpoint can split its routes across base URLs,
    which the client meets with a client per base URL, not with knowledge of Azure.
  - Azure's newer models are reasoning models, which reject `max_tokens` and `temperature`. A
    client that sends only `max_completion_tokens` and `reasoning_effort`, and only when set,
    serves the router and Azure alike.
  - Azure Government lists no transcription model, so audio on the IL6 path needs another
    source.
- **An agent reaches a capability its harness lacks through a tool.** A Go tool whose handler
  calls the direct client gave a Pi session a transcript, which the model used.
- Qwen3.8-27B, dense, decodes at about 12 tokens a second on the router.
- **Every harness drops an image a model can't take, quietly.** OpenCode replaces it with
  "ERROR: Cannot read image (this model does not support image input). Inform the user.", and
  the exchange succeeds with nothing telling the driver. Claude Code has no such case: every
  model it runs takes images.
- OpenCode's coding-agent system prompt confused Gemma 4 E4B, which reasoned that it had no
  tool to see an image and once answered that it couldn't, while holding the image.
- gpt-oss on the router fails, intermittently and under Pi and OpenCode alike, an exchange that
  needs a tool call and then a structured `respond`. Either llama.cpp rejects the malformed
  call ("does not match the expected peg-native format") or the model answers in text. On
  haiku and gpt-5-mini, every harness passes every capability. The harness can't make up for
  a model's tool calling.

## Infrastructure shape

The design held for three harnesses: Claude Code and OpenCode fit `harness.Connection` and
`harness/stdio` as Pi does, and no scenario names a harness outside a per-harness profile. It
stays provisional until step 6's workflow uses it.

- Six modules under a committed `go.work`, the packaging recommended for go-ai:
  - the core (`harness`, `harness/stdio`, `harness/filestore`, `harness/catalog`,
    `harness/cache`), with the standard library only;
  - `mcpbridge`, which holds the go-sdk dependency, imported only by the adapters that need
    MCP for tools;
  - one module per adapter (`pi`, `claude`, `opencode`), which carries its harness's baseline;
  - the programs that consume them (`clutch`).

  The go.mod files carry no `require` lines for workspace modules, since a placeholder version
  fails once a module also requires a real dependency. The cost is that the adapters and
  `clutch` build only inside the workspace.
- `model` is the second surface, beside `harness`: a direct client for OpenAI-compatible
  endpoints, standard library only, about 610 lines.
  - One `Config` of base URL, query, token source, and `http.Client` describes an endpoint. The
    caller's `http.Client` carries every timeout, so the package sets no policy.
  - The token source is a function. The spike's Azure source runs `az account get-access-token`
    in `clutch`'s composition root, so no Azure SDK enters the library.
  - Rejected: the official openai-go SDK, which would give up minimal-footprint for three POST
    shapes, and tau's `agent` shape, with its global registries, `map[string]any` options, and
    an `http.Client` built per request.
- The layers:
  - `harness` is transport-agnostic. It holds exchange scoping, sequencing, result folding,
    cancellation, exchange records, and the tool, skill, and schema types, over a five-method
    `Connection`. About 920 lines.
  - `harness/catalog` loads tools and skills from outside the program: skill trees from any
    `fs.FS` or from disk, and command tools from `tool.json` manifests. About 490 lines.
  - `harness/stdio` is the transport for line-protocol harnesses, about 600 lines. It answers
    requests the harness makes of the driver, sends a line that gets no response (`Notify`),
    and writes a command whose response comes much later, as ACP's prompt's does (`Send`).
  - `harness/cache` keeps the files a driver loads into a harness in a private,
    content-addressed directory. About 150 lines.
  - `mcpbridge` is about 760 lines over go-sdk v1.8.0 and jsonschema-go.
  - The adapters: `pi`, about 980 lines of Go and the 100-line `bridge.ts`; `claude`, about
    990; `opencode`, about 790.
  - Rejected: `coder/acp-go-sdk`, pre-1.0 and behind the ACP schema, for a codec of a few
    hundred lines over `harness/stdio`.
- Persistence adds two interfaces to `harness`:
  - `Store` keeps exchange records. `harness/filestore` is the spike's implementation, one JSON
    Lines file per session.
  - `Journal` is optional for a `Connection`: `Head`, and `Since(id)`. Only Pi's adapter keeps
    one (Sessions).
- The driving program, `clutch`, follows the Elemental CLI layout, which the Standards Lab
  note `context/cli-applications.md` records. Its scenarios each show one capability
  (`exchange`, `cancel`, `resume`, `tool`, `skill`, `structured`, `vision`, `embed`,
  `audio`), each harness's differences in a per-harness profile, and its `session` commands run
  one exchange or list the records, one process per call. `--tools` and `--skills` load external
  sources, and `clutch/examples` holds one of each.
- `clutch`'s tests are internal test packages, which Go Elemental's rule for external `_test`
  packages would move. `model`'s tests are external.
- `clutch conform` runs the nine capabilities over every harness and provider through one
  interface, with checks that compare structured responses rather than read the model's
  wording, and the harness versions it pins.

## Process lifecycle

- A driver that runs a harness as a process owns everything the harness starts. The harness runs
  in a process group of its own, and what is left of the group is killed when the harness exits.
  `service-runtime.md` covers the rejected alternatives.
- The same holds for a command tool: it runs in a process group of its own, which is killed once
  the command exits, so a script's leftover child neither outlives the call nor holds it open.
- The harness's stdout and stderr must be `*os.File` pipes. Any other writer makes exec copy it
  through a goroutine that `Wait` waits for, and a leftover child holding the pipe then holds up
  the exit.
- Line protocols are split on LF only. A JSON string may hold U+2028, and a line may exceed any
  fixed buffer.
- The driver's cache holds code the harness runs, the bridge, so it must belong to the current
  user and be writable by no one else. A directory under a shared temporary directory would let
  another local user plant a bridge under the name the driver trusts.
- A harness installed as "latest" changes under the driver. Pi 0.99 made `-ne` disable its
  built-in extensions too, among them the llama.cpp provider, and the driver's sessions then
  had no models at all. The driver now loads that provider with `-e builtin:llama.cpp` and needs
  Pi 0.99 or newer.
- Pi selects a model from a catalog it saved, not from the provider. A session starts from the
  catalog saved last and refreshes the provider's catalog in the background, which added a newly
  loaded model within about half a second. A `set_model` sent at start races that refresh, so
  the driver asks again for up to `Driver.CatalogWait` when Pi answers "Model not found" on
  llama.cpp.
  - For the router, with autoload off, the catalog holds only the models loaded when it was
    saved.
  - `pi update --models` doesn't refresh llama.cpp's catalog: the provider is an extension,
    present only in a session.
- Each harness keeps the user's own configuration out in its own way:
  - Claude Code: `--setting-sources ""` and `--strict-mcp-config`, with auto memory and
    self-updating off. Its bundled skills still load, and only `--bare` drops them, which
    refuses the subscription login.
  - OpenCode: `--pure`, XDG directories under the driver's state, and its configuration in
    `OPENCODE_CONFIG_CONTENT` with only the session's provider enabled. Its tools allowlist
    becomes deny rules, so the driver allows every permission request it is asked.
  - Pi: discovery off, and an agent directory of the driver's (`PI_CODING_AGENT_DIR`) when the
    session needs providers beyond the built-in ones.
  - A directory that holds configuration naming commands to run must be private to the user,
    as the cache is.
- OpenCode selects only a model its configuration or catalog lists. The driver lists the
  session's model in the configuration, so the model is selectable at once, with no catalog
  race.
- A native install changes under the driver too: Claude Code updated itself from 2.1.286 to
  2.1.287 during this step, outside the driver's sessions, and the conformance suite's version
  gate caught it.

## Harness comparison

The question the step answers for go-ai: which harness serves as a general-purpose harness
under Go, judged by the flexibility to reach models on many platforms, local included, and by
the control to make the harness the program's own. Going in, the assumption was Pi.

- **Models and platforms:**

  | | Local (llama.cpp router) | Cloud | Azure, keyless |
  |---|---|---|---|
  | Pi | Built-in provider, which reads the router's catalog and modalities | Any OpenAI-, Anthropic-, or Google-compatible endpoint in `models.json` | `apiKey` is `!az …`, run per request, so the token never goes stale |
  | OpenCode | An AI SDK provider, the model listed in the configuration | Broad, but Azure's reasoning models needed `@ai-sdk/openai`, since the compatible package sends `max_tokens` | A token fixed at open, which fails a session longer than about an hour |
  | Claude Code | Not supported | Claude models only | Subscription or cloud credentials |

- **Control:**
  - Pi: discovery off; tools in-process through an extension, with no second transport;
    explicit skills and `/skill:`; the model set over RPC; a journal with stable entry IDs;
    provider hooks; and a system prompt the driver replaces entirely
    (`--system-prompt`, or per run from an extension's `before_agent_start`).
  - Claude Code: capable, but closed. Its bundled skills stay, it caches a tool's schema by
    name, and it updates itself unless the driver stops it.
  - OpenCode: ACP is a standard protocol other agents speak too. But its coding-agent prompt of
    2,000 to 6,000 tokens comes with it, it adds about a second before each turn starts, and it
    can't create a session under a chosen ID.
- **Overhead** of one plain exchange, one sample each:

  | | Open | First text | Turn |
  |---|---|---|---|
  | Pi, router | 0.4 s | 0.9 s | 1.2 s |
  | Pi, Azure | 0.4 s | 2.9–3.6 s | 3.0–3.6 s |
  | Claude Code, Anthropic | 0.5 s | 1.3 s | 1.5–1.6 s |
  | OpenCode, router | 1.6 s | 12.0 s | 12.9 s |
  | OpenCode, Azure | 1.6–1.9 s | 1.7–2.2 s | 2.2–2.7 s |

  OpenCode's router turn includes its system prompt processed cold. Pi on Azure runs `az` for
  each request.
- **Stability:** Pi changed most under the driver (`-ne` in 0.99), and carries the most
  quirks the driver absorbs: cwd-scoped sessions, the catalog race, the quiet image drop. A
  pinned version is a requirement, not a nicety.
- **The finding:** the assumption holds. Pi is the most flexible and the most ownable, and is
  the default general-purpose harness, pinned. Claude Code suits work that needs Claude's
  quality and accepts Claude's models. OpenCode's value is ACP's portability more than the
  harness. Every harness has quirks of its own, so the choice comes down to the requirements
  at hand, and the `harness` surface lets a program make it per workload rather than bet on
  one.
