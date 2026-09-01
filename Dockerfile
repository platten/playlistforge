# syntax=docker/dockerfile:1.7

# Build the Vite application independently so the final Go compilation embeds
# only production assets and no JavaScript toolchain enters the runtime image.
FROM node:24-bookworm-slim AS web-build
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@11.19.0 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.27-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web-build /src/internal/webui/dist ./internal/webui/dist
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /out/playlist-forge ./cmd/playlistforge
RUN mkdir -p /runtime-config && touch /runtime-config/.keep && chown -R 65532:65532 /runtime-config

# Distroless supplies CA certificates for the OpenAI and Soundiiz HTTPS calls,
# has no package manager or shell, and runs as its built-in non-root user.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE_URL=https://github.com/unknown/playlist-forge
LABEL org.opencontainers.image.title="Playlist Forge" \
      org.opencontainers.image.description="Local AI playlist curator with Soundiiz handoff" \
      org.opencontainers.image.authors="Paul Pietkiewicz <773636+platten@users.noreply.github.com>" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="$SOURCE_URL" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"
COPY --from=go-build /out/playlist-forge /playlist-forge
COPY --from=go-build --chown=65532:65532 /runtime-config/ /config/
ENV PLAYLIST_FORGE_HOST=0.0.0.0 \
    PLAYLIST_FORGE_PORT=8787 \
    PLAYLIST_FORGE_OPEN_BROWSER=false \
    PLAYLIST_FORGE_CONFIG_DIR=/config \
    PLAYLIST_FORGE_LOG_FORMAT=json
USER nonroot:nonroot
VOLUME ["/config"]
EXPOSE 8787
ENTRYPOINT ["/playlist-forge"]
