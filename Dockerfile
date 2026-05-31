# syntax=docker/dockerfile:1
#
# Build from the REPOSITORY ROOT (this directory):
#
#   docker build -t moesekai-v2 .
#
# A .dockerignore keeps host-built web/node_modules and web/.next out of the
# context so they cannot clobber the Linux artifacts produced in the builders.
#
# Architecture: ONE process. The Go backend serves the statically-exported
# console SPA at "/" and the API/SSE/files at /api, /sse, /files. No nginx, no
# Node.js at runtime.

# ---- Stage 1: build (and statically export) the Next.js console ----
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund
COPY web/ .
ENV NEXT_TELEMETRY_DISABLED=1
# next.config sets `output: "export"` (prod), so this produces a static site in /web/out.
RUN npm run build

# ---- Stage 2: build the Go backend ----
FROM golang:1.25-alpine AS go-builder
WORKDIR /src
COPY server/go.mod server/go.sum* ./
RUN go mod download 2>/dev/null || true
COPY server/ .
# modernc.org/sqlite is pure Go, so CGO stays off.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /moesekai-server .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /moesekai-migrate ./cmd/migrate

# ---- Stage 3: runtime (Go only) ----
FROM alpine:3.20 AS runtime
# git is needed for the GitHub backup target; ca-certificates for HTTPS to the
# LLM / upstream / S3 endpoints.
RUN apk add --no-cache ca-certificates tzdata git && \
    git config --system --add safe.directory '*'
WORKDIR /app

# Backend binaries.
COPY --from=go-builder /moesekai-server ./moesekai-server
COPY --from=go-builder /moesekai-migrate ./moesekai-migrate

# Statically-exported console (served by the Go server at "/").
COPY --from=web-builder /web/out ./web

# Optional seed translations (used on first run when the DB is empty). This repo
# ships no translations/ dir, so the entrypoint detects the absent seed and
# starts with an empty DB; uncomment the COPY below if you add a seed tree.
# COPY translations/ ./seed-translations/
COPY docker-entrypoint.sh ./docker-entrypoint.sh
RUN chmod +x ./docker-entrypoint.sh

ENV DB_PATH=/data/moesekai.db \
    DATA_DIR=/data \
    WEB_DIR=/app/web

VOLUME ["/data"]
# The server listens on $PORT (default 8080; the platform may inject its own).
EXPOSE 8080

CMD ["./docker-entrypoint.sh"]
