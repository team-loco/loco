#!/bin/sh
set -eu

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
CREATE SCHEMA IF NOT EXISTS auth;
DO $$
BEGIN
    EXECUTE format('ALTER DATABASE %I SET search_path = auth, public', current_database());
END
$$;
SQL
exec "$@"
