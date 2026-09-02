#!/bin/sh
set -e

if [ -z "$DATABASE_URL" ]; then
  echo "ERROR: DATABASE_URL is not set" >&2
  exit 1
fi

# Extract the database name from the URL and build a connection URL to the default 'postgres' DB
# URL format: postgres://user:pass@host:port/dbname?params
DB_NAME=$(echo "$DATABASE_URL" | sed -n 's|.*/\([^?]*\).*|\1|p')
POSTGRES_URL=$(echo "$DATABASE_URL" | sed "s|/${DB_NAME}?|/postgres?|")

echo "Ensuring database '${DB_NAME}' exists..."
psql "$POSTGRES_URL" -tc "SELECT 1 FROM pg_database WHERE datname = '${DB_NAME}'" | grep -q 1 \
  || psql "$POSTGRES_URL" -c "CREATE DATABASE \"${DB_NAME}\""

# PRD-68 P6: migration ledger. Every file used to be replayed on every
# deploy, relying on idempotent SQL; now each filename is recorded after it
# applies and skipped next time. First run against an existing database
# replays everything once (still idempotent) and back-fills the ledger.
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
  "CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"

echo "Running database migrations..."
for f in /migrations/*.sql; do
  name=$(basename "$f")
  if psql "$DATABASE_URL" -tAc "SELECT 1 FROM schema_migrations WHERE filename = '${name}'" | grep -q 1; then
    echo "  Skipping (applied): ${name}"
    continue
  fi
  echo "  Applying: ${name}"
  # ON_ERROR_STOP so a failing statement fails the Job instead of silently
  # continuing to the next file with a half-applied schema.
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
    "INSERT INTO schema_migrations (filename) VALUES ('${name}') ON CONFLICT DO NOTHING"
done
echo "Migrations complete."
