############################
# STEP 0 build arguments
############################
# Pin the Go toolchain version.
ARG GO_VERSION=1.26.4
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
