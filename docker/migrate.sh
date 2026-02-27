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

echo "Running database migrations..."
for f in /migrations/*.sql; do
  echo "  Applying: $(basename "$f")"
  psql "$DATABASE_URL" -f "$f"
done
echo "Migrations complete."
