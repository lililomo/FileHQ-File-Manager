# syntax=docker/dockerfile:1
# FileManager — optional container image (SPEC ADR-20). The primary
# deployment is bin/filemanager + systemd behind aaPanel (docs/DEPLOY-AAPANEL.md);
# CI builds this image so the option keeps working.
#
#   docker build -f deploy/Dockerfile -t filemanager .        (from the repo root)
#   docker compose -f deploy/docker-compose.yml up -d
#
# Base image tags were checked on Docker Hub (2026-10-25); bump them together
# with backend/go.mod (toolchain) and web/ (Node).
ARG NODE_IMAGE=node:24.21.0-bookworm-slim
ARG GO_IMAGE=golang:1.27.3-bookworm
ARG RUNTIME_IMAGE=ubuntu:noble-20260930.1

# --- 1. frontend (Vite) --------------------------------------------------------
FROM ${NODE_IMAGE} AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@12.15.0 --activate
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# --- 2. single static binary with the frontend embedded ------------------------
FROM ${GO_IMAGE} AS go
WORKDIR /src/backend
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/web/dist/ ./internal/webassets/dist/
ARG VERSION=container
ARG COMMIT=unknown
RUN go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /out/filemanager ./cmd/filemanager

# --- 3. runtime: Ubuntu 24.04 with the same CLI tools as the server -------------
FROM ${RUNTIME_IMAGE}
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ffmpeg libvips-tools poppler-utils ripgrep 7zip ca-certificates curl tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 filemanager \
 && useradd --system --uid 10001 --gid filemanager --home-dir /nonexistent --no-create-home \
      --shell /usr/sbin/nologin filemanager \
 && mkdir -p /app/bin /storage \
 && chown filemanager:filemanager /storage \
 && chmod 0750 /storage
COPY --from=go /out/filemanager /app/bin/filemanager
WORKDIR /app
# One volume: DATA_DIR must share the filesystem with STORAGE_ROOT (rename
# into trash/versions, SPEC §3.3). The container listens on all interfaces;
# publish the port on 127.0.0.1 only and keep Nginx in front.
ENV APP_ENV=production \
    LISTEN_ADDR=0.0.0.0:8080 \
    STORAGE_ROOT=/storage \
    DATA_DIR=/storage/.system \
    TMPDIR=/storage/.system/tmp \
    GOMEMLIMIT=512MiB
USER filemanager:filemanager
VOLUME ["/storage"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s \
  CMD curl -fsS -o /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/bin/filemanager"]
CMD ["serve"]
