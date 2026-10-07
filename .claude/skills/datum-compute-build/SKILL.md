---
name: datum-compute-build
description: Use datumctl compute build (not raw kraft) to build and publish a container image for a Datum Compute Workload. Use this whenever a Compute Workload's image needs to be built or rebuilt from a Dockerfile and pushed to a registry, before touching the kraft CLI directly, and especially when the workload runs the unikernel runtime class.
---

# Building images for Datum Compute

Datum Compute Workloads (general-purpose or unikernel runtime class) run
images in Datum's own format, not a plain OCI container image pulled as-is.
A unikernel-class image in particular carries `os: kraftcloud` in its
manifest platform and will fail to pull under the `general-purpose` runtime
class (and vice versa isn't guaranteed either) — building the right format
for the target runtime class matters.

## Default to `datumctl compute build`

When a Workload's image needs to be (re)built from a Dockerfile and pushed:

```bash
datumctl compute build --push --output <registry-ref> <build-context-dir>
```

`datumctl compute deploy --build --image=<registry-ref> ...` does the same
build-and-push as one step of a full `deploy`, when you're also changing the
Workload itself in the same command.

Don't reach for the raw `kraft` CLI (`kraft build`, `kraft pkg`, `kraft cloud
deploy`, `kraft cloud compose build`) for this unless the user explicitly asks
for it or a Kraftfile is already present and in play. Reasons:

- Plain `kraft build`/`kraft pkg` require a Kraftfile describing the unikernel
  target. A typical Dockerfile-based app (e.g. a Node/Python/Go service with
  no unikernel-specific build config) has none, and these commands refuse to
  guess: `could not determine what or how to build from the given context`.
- The commands that *do* understand a plain Dockerfile
  (`kraft cloud deploy`, `kraft cloud compose build/push`) are scoped to
  **Unikraft Cloud**, a separate platform from Datum Compute. They need their
  own Unikraft Cloud account token (distinct from any Datum or registry
  credentials already configured) and will provision resources there, not on
  Datum — not what you want when the goal is a Datum Compute Workload.
- `datumctl compute build` is Datum's own supported path: it takes the same
  Dockerfile and build context as a normal container build, converts it into
  whichever Compute-compatible format the target needs (including the
  `kraftcloud`-platform unikernel image), and pushes straight to the registry
  ref you give it — no separate cloud account, no Kraftfile required for the
  common case.
- Advanced users who already have a Kraftfile can still pass it with
  `datumctl compute build --kraftfile ./Kraftfile .`, which delegates to the
  unikraft CLI under the hood — but that's the exception, not the default.

## If the build fails looking for BuildKit

`datumctl compute build` needs a BuildKit backend and will say so plainly if
it can't find one:

```
could not connect to BuildKit: set BUILDKIT_HOST, start buildkitd, or enable Docker's BuildKit backend
```

Check for one before assuming none exists — `docker ps` may already show a
standalone `buildkitd` container (some environments keep one running for
exactly this purpose). If so, point the build at it:

```bash
BUILDKIT_HOST=tcp://127.0.0.1:<port> datumctl compute build --push --output <registry-ref> .
```

Otherwise, start one (`docker buildx create --use`, or run
`moby/buildkit` directly) rather than falling back to the raw `kraft` CLI —
the BuildKit requirement is the same either way, it's just about getting a
backend wired up.

## After pushing: update the Workload, don't recreate it

An image's digest is a mutable field on an existing Workload — update
`spec.template.spec.runtime.sandbox.containers[].image` to the new digest
(`workloads` MCP tool / `datumctl compute deploy`'s own image-bump path) and
let it roll out, rather than deleting and recreating the Workload.

`runtime.class` (general-purpose vs. unikernel) **is** immutable once set — if
the image's required runtime class is actually changing, not just its
digest, the Workload has to be deleted and recreated (or deployed under a new
name), not updated in place.

## Don't mistake a cold start for a failure

The first request to a fresh unikernel instance after an image swap can take
several seconds to tens of seconds (snapshot boot), even while the Workload
and NetworkService already report `Ready`/healthy — that status reflects
control-plane state, not whether the edge has actually served a request yet.
Retry with a longer timeout, or check `alb_traffic_summary`/`alb_diagnose`
(if the `datum-mcp` ALB tools are available) for real traffic evidence,
before concluding the rollout is broken.
