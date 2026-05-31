#!/bin/sh
set -eu

DB_PATH="${DB_PATH:-/data/moesekai.db}"
DATA_DIR="${DATA_DIR:-/data}"
SEED_DIR="/app/seed-translations"
PORT="${PORT:-8080}"

echo "=== MOESEKAI v2 STARTUP ==="
echo "DB_PATH:  $DB_PATH"
echo "DATA_DIR: $DATA_DIR"
echo "PORT:     $PORT (Go: serves console SPA + /api + /sse + /files)"

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

# The Go server is the only process: it serves the static console and the API on
# one port. exec replaces the shell so signals reach Go directly (clean shutdown)
# and its tagged, timestamped logs go straight to `docker logs`.
echo "Starting moesekai-server on :${PORT}..."
exec ./moesekai-server
