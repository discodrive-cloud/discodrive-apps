# Reproducible Linux build for the DiscoDrive Wails client.
#
# Wails v2 cannot cross-compile a Linux GUI from macOS (needs the WebKitGTK
# toolchain), and there is no official wailsapp/wails image — so we build inside
# Debian. Builds for the image's native architecture (linux/arm64 on Apple
# Silicon, linux/amd64 on Intel/CI). Pass `--platform=linux/amd64` to `docker
# build` to force amd64 (emulated, slower, on Apple Silicon).
#
# Debian bookworm ships only webkit2gtk-4.1 (4.0 was dropped), so the build uses
# Wails' `webkit2_41` tag.
#
# Build + export the binary to ./dist/linux:
#   docker build -f scripts/linux.Dockerfile -o type=local,dest=dist/linux .

FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251 AS build

# Keep in step with GO_VERSION in .github/workflows/release.yml. The checksums are
# go.dev's published sha256 for this version, verified before unpacking.
ARG GO_VERSION=1.26.8
ARG GO_SHA256_AMD64=d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b
ARG GO_SHA256_ARM64=211ffced9dcb9633a55eac6364816ec0ddd951389a740e88fa8b3337971bdda0
ARG WAILS_VERSION=v2.12.0
ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git build-essential pkg-config \
      libgtk-3-dev libwebkit2gtk-4.1-dev nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# Go toolchain (architecture-aware: amd64 / arm64).
RUN ARCH="$(dpkg --print-architecture)" \
    && case "$ARCH" in amd64) SUM="$GO_SHA256_AMD64";; arm64) SUM="$GO_SHA256_ARM64";; *) exit 1;; esac \
    && curl -fsSL -o /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" \
    && echo "$SUM  /tmp/go.tgz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz

RUN go install github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}

WORKDIR /src
COPY . .
WORKDIR /src/daemon/cmd/discodrive-wails
RUN wails build -tags webkit2_41 -clean

# Export stage: contains only the built binary so `-o type=local` writes a clean dir.
FROM scratch AS export
COPY --from=build /src/daemon/cmd/discodrive-wails/build/bin/ /
