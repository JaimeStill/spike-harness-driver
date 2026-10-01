# deploy

`Containerfile` builds the image a service built on the spike would ship: `clutch` and the Pi
version `clutch conform` pins, with `tini` as PID 1. Build it from the repository root, since
the Go build needs every module under `go.work`:

```sh
docker build -f deploy/Containerfile -t clutch .
```

It has two runtime targets:

- **`standalone`**, the default, uses Pi's standalone release: one executable with its runtime
  bundled, and its assets beside it. The download is checked against the SHA-256 from the
  release's `SHA256SUMS`, so moving the pin means changing `PI_VERSION` and `PI_SHA256`
  together.
- **`node`** (`--target node`) installs Pi from npm on `node:22-slim`. It exists to compare
  footprints.

Neither image carries Claude Code or OpenCode. The image carries only the harness its
workflows run on, and Pi is the default (`context/findings.md`, Harness comparison).

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
  (`context/findings.md`, Deployment).
- **Stopping.** `docker stop` sends SIGTERM, which `tini` passes to `clutch`. `clutch` stops its
  runs without ending them, closes the event streams, and exits. The next start resumes the
  runs.
- **`tini`** reaps any process a harness leaves behind. The driver kills each harness's process
  group, but nothing kills it if the driver itself is killed.
