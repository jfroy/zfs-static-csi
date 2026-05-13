# syntax=docker/dockerfile:1.24

ARG GO_VERSION=1.26
ARG DEBIAN_VERSION=trixie

# --platform=$BUILDPLATFORM keeps the Go toolchain native; cross-compilation
# to TARGETARCH is handled by `go build` itself, no QEMU required.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-${DEBIAN_VERSION} AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/zfs-static-csi ./cmd/zfs-static-csi

# Driver chroots into /host to use the host's zfs(8); no userspace bundled.
FROM gcr.io/distroless/static-debian13:latest
COPY --from=builder /out/zfs-static-csi /usr/local/bin/zfs-static-csi
ENTRYPOINT ["/usr/local/bin/zfs-static-csi"]
