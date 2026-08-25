# Device-side integration contract

The Dockerfile frontend cannot discover the macOS Device Firewall CA or read
host environment variables. The existing `dff` command shim is the client-side
half of the integration.

## Locality gate

The shim must modify a build only when all of these are true:

1. the active Docker context uses a local Unix socket;
2. every selected Buildx node is a local Docker Desktop `docker` or
   `docker-container` worker;
3. Device Firewall capture, authentication, and CA health are ready; and
4. the Dockerfile selected the DepthFirst frontend, or the user explicitly
   enabled it through `BUILDKIT_SYNTAX`.

Remote, SSH, TCP, Kubernetes, mixed-node, and indeterminate builders pass
through unchanged. They must receive no local CA path, readiness check, or
DepthFirst output.

## Inputs

For a protected local build, append:

- `--secret id=DF_FIREWALL_CA,src=<validated public CA path>`
- `--build-arg DF_FIREWALL_CA_SHA256=<sha256 of those exact bytes>`

Resolve and hash the certificate once. Do not check one path and later pass a
newly resolved path. The frontend requires the secret when the fingerprint is
present and emits a cache-key-distinct CA installation command.

Do not pass the device API key into a local build. Existing transparent capture
and the daemon continue to rewrite registry requests and inject authentication.

## Command surface

The integration should cover:

- `docker build`, `docker builder build`, and `docker image build`
- `docker buildx build` and `docker buildx b`
- `docker buildx bake` and `docker buildx f`
- `docker compose build`, normalized through `docker compose build --print`
  and executed as Bake with wildcard secret and build-argument overrides

`docker compose up --build` is intentionally outside the first release.

For Bake, append the CA secret and fingerprint to every selected target without
replacing existing secrets, arguments, cache, output, SSH, provenance, or SBOM
configuration.

## Failure policy

If a definitely local protected build cannot validate the CA or firewall
health, fail closed with one actionable error. An explicit user-selected
fail-open mode may run the original Docker command exactly unchanged. Never
silently label an unchanged build as protected.
