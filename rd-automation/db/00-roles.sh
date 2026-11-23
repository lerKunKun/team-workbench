#!/bin/sh
# docker-entrypoint runs this on an empty PGDATA only.
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=app_password="$APP_DB_PASSWORD" --set=n8n_password="$N8N_DB_PASSWORD" <<'SQL'
BEGIN;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE ROLE rd_app LOGIN PASSWORD :'app_password';
CREATE ROLE rd_n8n LOGIN PASSWORD :'n8n_password';
-- n8n issues CREATE SCHEMA IF NOT EXISTS on every startup, even when it already owns it.
GRANT CREATE ON DATABASE rd_platform TO rd_n8n;
CREATE SCHEMA app AUTHORIZATION rd_app;
CREATE SCHEMA n8n AUTHORIZATION rd_n8n;
COMMIT;
SQL
