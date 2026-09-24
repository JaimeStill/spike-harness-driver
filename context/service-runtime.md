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
    or an MCP server (`adapters.md`).
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
