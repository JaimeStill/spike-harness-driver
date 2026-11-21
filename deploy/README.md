# deploy

`Containerfile` builds the image a service built on the spike would ship: `clutch` and the Pi
version `clutch conform` pins, with `tini` as PID 1. Build it from the repository root, since
the Go build needs every module under `go.work`:

```sh
docker build -f deploy/Containerfile -t clutch .                    # Pi's standalone release
docker build -f deploy/Containerfile --target node -t clutch:node .  # Pi from npm, on Node
```

The Containerfile has two build targets, which differ in how they install Pi:

- **`standalone`**, the default, installs Pi's standalone release on the shared `base` stage:
  one executable with its runtime bundled, and its assets beside it. The download is checked against
  the SHA-256 of `pi-linux-x64.tar.gz` from the release's `SHA256SUMS`, so moving the pin means
  changing `PI_VERSION` and `PI_SHA256` together. The checksum pins the download, as `mise.toml`
  pins the version on a workstation. The base images are pinned by index digest as well as tag,
  in the `*_IMAGE` arguments, so a rebuild gets the same bytes; moving one means updating both.
- **`node`** (`--target node`) installs Pi from npm on `node:22-slim`. It exists to compare
  footprints. Pi asks for Node 22.19 or newer.

Both targets build `clutch` statically linked (`CGO_ENABLED=0`), so the runtime needs no Go and
no libc of its own for it. Every runtime has `tini` as PID 1, CA certificates for the cloud
endpoints, and an unprivileged user whose state lives on a volume. The state directory holds code
the harness runs, so it belongs to that user alone (mode `0700`).

Neither image carries Claude Code or OpenCode, because an image carries only the harness its
workflows run on, and Pi is the default
([`docs/findings.md`](../docs/findings.md#harness-comparison), Harness comparison).

## The build context

`.dockerignore` keeps what the image doesn't use out of the build context: the image builds
`clutch` from the Go sources alone.

## Running it

The image runs `clutch serve` as an unprivileged user. Runs, exchange records, Pi's sessions,
and the files the driver loads into Pi all live in `/var/lib/clutch`, which should be a volume
so that a restarted container resumes its unfinished runs.

```sh
docker run -d --name clutch -p 127.0.0.1:8080:8080 \
  -e LLAMA_BASE_URL -v clutch-state:/var/lib/clutch clutch
curl -X POST --data-binary @clutch/examples/workflows/review.json localhost:8080/runs
curl -N localhost:8080/runs/<id>/events
```

- **The server has no authentication.** Inside the container it listens on every interface, so
  publish its port to loopback only, or put an authenticating proxy in front of it.
- **The model endpoint.** `LLAMA_BASE_URL` points at the router. A tailnet hostname resolves
  with `--network host` on a host that runs Tailscale. `AZURE_OPENAI_BASE_URL` points at Azure,
  but Azure through Pi runs `az` for its token, and the image has no `az`
  ([`docs/findings.md`](../docs/findings.md#deployment), Deployment).
- **Stopping.** `docker stop` sends SIGTERM, which `tini` passes to `clutch`. `clutch` stops its
  runs without ending them, closes the event streams, and exits, and the next start resumes the
  runs. `clutch` allows itself 30 seconds for that, and Docker's default grace is 10, so stop it
  with `docker stop -t 35` (or `--stop-timeout 35` on `docker run`). A container killed sooner
  still resumes its runs; it only loses the clean stop.
- **One state directory, one process.** The run log refuses an event out of sequence, so a second
  process driving the same run fails rather than corrupting the log, but nothing coordinates two
  processes. Give each container its own volume.
- **`tini`** reaps any process a harness leaves behind. The driver kills each harness's process
  group, but if the driver itself is killed, nothing does.
