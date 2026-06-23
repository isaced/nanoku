# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26
ARG NODE_VERSION=20

FROM node:${NODE_VERSION}-bookworm-slim AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci
COPY ui/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS go
ARG VERSION=docker
ARG COMMIT=docker
ARG DATE
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY --from=ui /src/internal/api/dist ./internal/api/dist
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)} -X main.buildType=release" \
    -o /out/nanoku .

FROM docker:27-cli AS docker-cli

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=go /out/nanoku /usr/local/bin/nanoku

ENV NANOKU_LISTEN=:8080 \
    NANOKU_DB=/data/nanoku.db \
    NANOKU_CADDYFILE=/data/Caddyfile \
    NANOKU_COMPOSE_DIR=/data/composes

USER nonroot
WORKDIR /data
VOLUME /data

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/nanoku"]
