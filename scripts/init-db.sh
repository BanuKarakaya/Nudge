#!/bin/sh
# Bootstrap ONLY a new, empty database. Never executes Goose Down sections.
# Docker's PostgreSQL entrypoint invokes this only on a fresh data volume.
# Native use: DATABASE_URL=... sh scripts/init-db.sh
(
set -eu

migrations_dir=${NUDGY_MIGRATIONS_DIR:-/nudgy-migrations}
if [ ! -d "$migrations_dir" ]; then
    migrations_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../migrations" && pwd)
fi

if [ -n "${DATABASE_URL:-}" ]; then
    set -- --dbname="$DATABASE_URL"
else
    set --
    export PGUSER="${PGUSER:-${POSTGRES_USER:-nudge}}"
    export PGDATABASE="${PGDATABASE:-${POSTGRES_DB:-nudge}}"
fi

for migration in 001_init.sql 002_raindrop_token_expiry.sql 003_feedbin.sql; do
    test -r "$migrations_dir/$migration"
done

{
    for migration in 001_init.sql 002_raindrop_token_expiry.sql 003_feedbin.sql; do
        sed '/^-- +goose Down/,$d' "$migrations_dir/$migration"
    done
} | psql "$@" -X --set=ON_ERROR_STOP=1 --single-transaction --file=-
)
