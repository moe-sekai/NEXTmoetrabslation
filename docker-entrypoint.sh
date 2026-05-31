#!/bin/sh
set -eu

DB_PATH="${DB_PATH:-/data/moesekai.db}"
DATA_DIR="${DATA_DIR:-/data}"
SEED_DIR="/app/seed-translations"

echo "=== MOESEKAI v2 STARTUP ==="
echo "DB_PATH:  $DB_PATH"
echo "DATA_DIR: $DATA_DIR"

mkdir -p "$DATA_DIR"

# On first run (no DB yet), seed translations from the image into SQLite.
if [ ! -f "$DB_PATH" ] && [ -d "$SEED_DIR" ]; then
  echo "No database found; migrating seed translations into SQLite..."
  ./moesekai-migrate -src "$SEED_DIR" -db "$DB_PATH" -verify=false || {
    echo "WARNING: seed migration failed; starting with empty database"
  }
else
  echo "Database present or no seed; skipping migration."
fi

# Start the Next.js console in the background.
echo "Starting console (Next.js) on :${WEB_PORT:-3000}..."
( cd /app/web && PORT="${WEB_PORT:-3000}" BACKEND_ORIGIN="${BACKEND_ORIGIN:-http://localhost:9090}" npx next start -p "${WEB_PORT:-3000}" ) &

# Start the Go backend in the foreground (PID 1 semantics for the container).
echo "Starting backend (Go) on :${PORT:-9090}..."
exec ./moesekai-server
