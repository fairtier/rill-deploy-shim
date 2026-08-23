############################
# STEP 0 build arguments
############################
# Pin the Go toolchain to a MINOR version, not a full patch version. This repo
# has no Dependabot, and the image is only rebuilt on a release tag, so a full
# patch pin here is a value nothing updates: 1.26.4 sat in this ARG long enough
# to miss the 1.26.5/1.26.6 stdlib batch (GO-2026-5026, GO-2026-5972,
# GO-2026-6088..6091) and kept shipping an unpatched binary. `1.27` resolves to
# the newest 1.27.x at build time, which is what you want when builds are rare.
# Move it with the `go` directive in go.mod.
ARG GO_VERSION=1.27
ARG BASE_VARIANT=trixie

############################
# STEP 1 build the binary
############################
FROM golang:${GO_VERSION}-${BASE_VARIANT} AS builder

LABEL maintainer="Tomas Prochazka <tomas.prochazka5d@gmail.com>"

WORKDIR /app

ENV GOOS=linux \
    GOARCH=amd64 \
    CGO_ENABLED=0

# No external dependencies (stdlib only), so there is no go.sum and no
# `go mod download` step — copy the source and build.
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -o /out/rill-deploy-shim .

############################
# STEP 2 build a small image
############################
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/rill-deploy-shim /bin/rill-deploy-shim

USER nonroot:nonroot

EXPOSE 9009

ENTRYPOINT ["/bin/rill-deploy-shim"]
