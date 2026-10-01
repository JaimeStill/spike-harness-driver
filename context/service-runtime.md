# Service runtime

Planned for step 6, where a service runs workflows over many sessions in a container.

- **Process cleanup.** `harness/stdio` kills the harness's process group once the harness
  exits. Rejected alternatives:
  - Leaving cleanup to the harness. It's portable, but a harness that is killed or crashes
    cleans up nothing.
  - Linux's `Pdeathsig`. It reaches only the direct child.
  - Relying on the container alone. The runtime kills the process tree only when the container
    stops, so a service that runs many sessions would gather orphans in between.

  The image should still run an init such as `tini` to reap orphans, as the last backstop.
- **Windows.** The process-group hooks do nothing there, so leftovers outlive the harness. A job
  object that kills everything in it when the job closes (`golang.org/x/sys/windows`) is the fix,
  if Windows ever matters.
- **Exchange records.**
  - A service keeps its records in its database behind `harness.Store`, instead of
    `harness/filestore`. A torn last line in a file store blocks that session's resume.
  - A session records each exchange synchronously in its event loop, so a slow store delays
    the next `Send`.
  - Pi's `Head` reads every entry of the session on each open, so a long-running session needs
    bounded journal reads.
  - `clutch`'s `--state` defaults to the user's cache directory, which a service replaces
    with storage of its own. It must be private to the service, because the harness runs code
    from the cache in it.
- **Unread exchanges.** An exchange whose events no one reads keeps them, and a goroutine, until
  its session closes. A long-running coordinator may need a way to drop an exchange it only
  Waits on.
- **Tool and skill sources.**
  - A database as a registry: it chooses tools and skills per tenant or per workflow, and
    versions them. A skill source is an `fs.FS` over its rows, which `harness/catalog` reads
    unchanged. A stored tool definition names code that lives elsewhere, an executable on disk
    or an MCP server (`findings.md`, Payloads).
  - Layered skill search paths with precedence, such as user, workspace, and project, where a
    nearer skill of the same name overrides a farther one, as Pi's own discovery does. `--skills`
    is repeatable today but treats a shared name as an error.
  - Command tools inherit the service's whole environment, provider keys included, and need an
    allowlist.
  - `HarnessTools: nil` keeps the harness's default tools, which for Pi follow the user's
    `defaultTools` setting, so a service should always name its allowlist.
  - A crash while the cache writes can leave a `.write-*` directory behind, which needs a sweep.
- **Model and provider failures.**
  - A model the router loads on demand can take minutes to its first token. `Send` has no
    deadline of its own, which suits long agent work, so a service needs progress or a heartbeat
    to tell a loading model from a hung one.
  - A provider error, such as llama.cpp rejecting a malformed tool call, ends the exchange with
    `stopReason: "error"`, and Pi doesn't retry it. A service decides which of these to retry.
- **The harness's version.** The image pins each harness it carries, and `clutch conform` checks
  the pins (`mise.toml` for Pi and OpenCode). A harness installed as "latest" changed what the
  driver's flags do between two runs, and Claude Code, a native install, updated itself outside
  the driver's sessions (`findings.md`, Process lifecycle). In an image, the install itself is
  pinned; the driver only keeps the sessions it starts from updating.
- **Subscription usage limits.** A Claude Code session on a subscription reaches its limits. A
  service pauses a workflow at a `*harness.LimitError` until its `ResetsAt` rather than failing
  it, and watches `EventLimit` notices to slow down before the limit.
- **Context-window compaction.** Each harness compacts a long session on its own; the adapters
  pass the markers through as `EventHarness`. A long-running workflow needs to know what a
  compaction drops, per harness, before it relies on a session's memory.
- **The system prompt.** Pi's can be replaced entirely (`--system-prompt`, or per run from an
  extension's `before_agent_start`). Claude Code takes `--system-prompt` too, and OpenCode an
  agent's prompt in its configuration; neither was probed. A `SystemPrompt` option on
  `harness.Options` would let a workflow give each session its role.
- **Forcing `respond`.** A structured response depends on the model choosing to call `respond`,
  which gpt-oss on the router doesn't reliably do after a tool call. Forcing it with the
  provider's `tool_choice`, through Pi's `before_provider_request` hook, would be sturdier;
  the payload's shape differs per provider.
- **Native capability models.**
  - The router's vision, embedding, and audio models share the GPU pool with the text models.
    A vision model beside gpt-oss leaves no room for the others, so a service either sizes the
    set it keeps loaded or swaps models, and a swap must land in the harness's model catalog
    before a session selects the model.
  - Audio is split on the client: Gemma 4 E4B loops on clips longer than about 30 seconds, and
    Azure's transcription takes files up to 25 MB.
- **Harness credentials.**
  - Pi takes a short-lived token as a command it runs for each request (`!az …`), so a long
    session never holds an expired one.
  - OpenCode's configuration takes no command, so its token is fixed when the session opens and
    a session longer than about an hour fails. It also sits in `OPENCODE_CONFIG_CONTENT`, which
    every process OpenCode starts, its shell tool's included, inherits: no wider than the Azure
    CLI's own access, but a service gives OpenCode a token scoped to the model alone.
  - The driver's MCP server for OpenCode listens on loopback behind a per-session bearer
    token, since any local user can reach a loopback port and the tools run with the service's
    privileges.
- **Model client credentials.**
  - The spike's Azure token comes from the Azure CLI's sign-in. A service takes a managed
    identity instead, either from IMDS over plain HTTP or through an azidentity sub-module,
    behind the same token-source function.
  - An error from a failed request quotes the endpoint's URL. The endpoint isn't a secret, but
    a service redacts it from the errors it logs.
