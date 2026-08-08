FROM litestream/litestream:0.5.9 AS litestream
# Generate assets and compile Go on the Buildx host rather than in an emulated
# target container. The final go build explicitly selects the target below.
FROM --platform=$BUILDPLATFORM ghcr.io/danielmichaels/ci-tailwind:2026-08-05 AS tailwind
# ci-templ:2026-08-05 ships templ v0.3.1020, matching the version pinned in
# go.mod. Keep the two versions in step when either one changes.
FROM --platform=$BUILDPLATFORM ghcr.io/danielmichaels/ci-templ:2026-08-05 AS templ
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# Only the module files first, so dependency download caches independently of
# source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

# Copied from ci-tailwind rather than curl'd here: its glibc build needs a
# Debian builder, not Alpine — see danielmichaels/ci-images README.
COPY --from=tailwind /usr/local/bin/tailwindcss /usr/local/bin/tailwindcss
COPY --from=templ /go/bin/templ /usr/local/bin/templ

# Both generators are guarded rather than templated: the files they consume
# only exist for some generated configurations.
RUN if [ -f ./assets/css/input.css ]; then \
      tailwindcss -i ./assets/css/input.css -o ./assets/static/css/main.css --minify; \
    fi

# templ output is generated, never committed, so it must be produced here too.
# The prebuilt binary avoids compiling the generator once per target platform.
RUN if ls ./internal/ui/templates/*.templ >/dev/null 2>&1; then templ generate; fi

# Injected by CI; the fallbacks keep a bare `docker build` working.
ARG VERSION=dev
ARG REVISION=unknown

# The module path comes from go.mod rather than being baked in, so the ldflags
# stay correct if the module is ever renamed.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    MODULE=$(go list -m) && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -trimpath \
      -ldflags="-s -w \
        -X ${MODULE}/internal/version.Version=${VERSION} \
        -X ${MODULE}/internal/version.Revision=${REVISION}" \
      -o /out/app \
      ./cmd/...

# The debug variant, not static: Litestream has to wrap the process with
# `replicate -exec`, and that entrypoint needs a shell.
FROM gcr.io/distroless/base-debian12:debug-nonroot
WORKDIR /app

COPY --from=litestream ["/usr/local/bin/litestream", "/usr/local/bin/litestream"]
COPY --chmod=755 ["entrypoint", "/app/entrypoint"]
COPY --from=builder ["/out/app", "/usr/bin/app"]
# /etc/litestream.yml is where litestream looks by default; anywhere else
# needs -config passing to every invocation.
COPY ["litestream.yml", "/etc/litestream.yml"]

ENV DOCKER=1
EXPOSE 9898

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/bin/app", "healthcheck"]

# The interpreter is named explicitly because this image has no /bin/sh: the
# debug variant ships busybox at /busybox/sh and leaves /bin empty, so the
# script's own `#!/bin/sh` cannot resolve. Keeping the shebang portable means
# the same script still runs locally.
ENTRYPOINT ["/busybox/sh", "/app/entrypoint"]
