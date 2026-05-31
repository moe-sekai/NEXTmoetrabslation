# syntax=docker/dockerfile:1
#
# Build from the REPOSITORY ROOT so both v2/ sources and the root-level
# translations/ seed are in context:
#
#   docker build -f v2/Dockerfile -t moesekai-v2 .
#
# (Building with v2/ as context will fail: translations/ lives at the root.)

# ---- Stage 1: build the Next.js console ----
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY v2/web/package.json v2/web/package-lock.json* ./
RUN npm install --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund
COPY v2/web/ .
# Console talks to the backend on the same origin via ingress/proxy in prod.
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# ---- Stage 2: build the Go backend ----
FROM golang:1.23-alpine AS go-builder
WORKDIR /src
COPY v2/server/go.mod v2/server/go.sum* ./
RUN go mod download 2>/dev/null || true
COPY v2/server/ .
# modernc.org/sqlite is pure Go, so CGO stays off.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /moesekai-server .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /moesekai-migrate ./cmd/migrate

# ---- Stage 3: runtime ----
FROM node:20-alpine AS runtime
RUN apk add --no-cache ca-certificates tzdata git && \
    git config --system --add safe.directory '*'
WORKDIR /app

# Backend binaries.
COPY --from=go-builder /moesekai-server ./moesekai-server
COPY --from=go-builder /moesekai-migrate ./moesekai-migrate

# Next.js standalone-style runtime: copy build output + node_modules.
COPY --from=web-builder /web/.next ./web/.next
COPY --from=web-builder /web/public ./web/public
COPY --from=web-builder /web/node_modules ./web/node_modules
COPY --from=web-builder /web/package.json ./web/package.json
COPY --from=web-builder /web/next.config.ts ./web/next.config.ts

# Seed translations (used on first run when the DB is empty).
COPY translations/ ./seed-translations/
COPY v2/docker-entrypoint.sh ./docker-entrypoint.sh
RUN chmod +x ./docker-entrypoint.sh

ENV PORT=9090 \
    DB_PATH=/data/moesekai.db \
    DATA_DIR=/data \
    WEB_PORT=3000 \
    BACKEND_ORIGIN=http://localhost:9090

VOLUME ["/data"]
EXPOSE 9090 3000

CMD ["./docker-entrypoint.sh"]

