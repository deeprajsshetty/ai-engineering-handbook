# One immutable image for every environment (public or private cloud).
# Configuration and secrets are injected at runtime; nothing environment-
# specific is baked in. Base images are pinned by tag; pin by digest in CI
# (Renovate can maintain both).

FROM node:24.21.0-alpine AS web
WORKDIR /src/platform/web
COPY platform/web/package.json platform/web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY platform/web/ ./
RUN npm run build

FROM golang:1.27.4-alpine AS server
WORKDIR /src/platform
COPY platform/go.mod platform/go.sum ./
RUN go mod download && go mod verify
COPY platform/ ./
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/deeprajsshetty/observability-platform/platform/internal/api.Version=${VERSION} -X github.com/deeprajsshetty/observability-platform/platform/internal/api.Commit=${COMMIT}" \
      -o /out/obsplatform ./cmd/obsplatform

# Static, distroless, non-root runtime: no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="obsplatform" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.description="Observability platform MVP: discovery, topology, topology-driven metrics"
COPY --from=server /out/obsplatform /obsplatform
COPY --from=web /src/platform/web/dist /web
ENV OBS_WEB_DIR=/web OBS_HTTP_ADDR=:8080
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/obsplatform"]
CMD ["serve"]
