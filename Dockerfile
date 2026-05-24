# Build stage
FROM --platform=$BUILDPLATFORM golang:1.22 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -a -ldflags="-s -w" -o manager cmd/main.go

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

# OCI image labels. goreleaser overrides these with the real values at
# release time via --label flags; the defaults here keep hand-built
# images discoverable.
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/example/secret-distribution-operator" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="secret-distribution-operator" \
      org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
