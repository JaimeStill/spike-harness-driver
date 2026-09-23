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
- **Unread exchanges.** An exchange whose events no one reads keeps them, and a goroutine, until
  its session closes. A long-running coordinator may need a way to drop an exchange it only
  Waits on.
