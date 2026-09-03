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
# applies and skipped next time.
#
# Bootstrap: an EXISTING database (benchmark_runs present) with an empty
# ledger is a pre-ledger deployment. Not every historical file is strictly
# idempotent (001_initial.sql has bare CREATE TABLE), so the very first pass
# replays them the old lenient way (errors ignored, exactly as before) and
# back-fills the ledger. From then on every run is strict: a failing
# statement fails the Job instead of silently continuing.
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
  "CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"

LEDGER_COUNT=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM schema_migrations")
HAS_SCHEMA=$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM information_schema.tables WHERE table_name = 'benchmark_runs'")
STRICT=1
if [ "$LEDGER_COUNT" = "0" ] && [ "$HAS_SCHEMA" != "0" ]; then
  echo "Existing schema with empty ledger: bootstrap pass (lenient replay, back-filling schema_migrations)."
  STRICT=0
fi

echo "Running database migrations..."
for f in /migrations/*.sql; do
  name=$(basename "$f")
  if psql "$DATABASE_URL" -tAc "SELECT 1 FROM schema_migrations WHERE filename = '${name}'" | grep -q 1; then
    echo "  Skipping (applied): ${name}"
    continue
  fi
  echo "  Applying: ${name}"
  if [ "$STRICT" = "1" ]; then
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f"
  else
    psql "$DATABASE_URL" -f "$f"
  fi
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
    "INSERT INTO schema_migrations (filename) VALUES ('${name}') ON CONFLICT DO NOTHING"
done
echo "Migrations complete."
