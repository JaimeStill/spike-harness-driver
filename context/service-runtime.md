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
  - `clutch`'s `--state` defaults to a temporary directory, which a service replaces with its
    own storage.
- **Unread exchanges.** An exchange whose events no one reads keeps them, and a goroutine, until
  its session closes. A long-running coordinator may need a way to drop an exchange it only
  Waits on.
