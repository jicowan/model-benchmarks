#!/bin/sh
set -e

if [ -z "$DATABASE_URL" ]; then
  echo "ERROR: DATABASE_URL is not set" >&2
  exit 1
fi

echo "Running database migrations..."
for f in /migrations/*.sql; do
  echo "  Applying: $(basename "$f")"
  psql "$DATABASE_URL" -f "$f"
done
echo "Migrations complete."
