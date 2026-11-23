#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -e .env ] || [ -L .env ]; then
  echo '.env already exists; leaving it untouched.'
  exit 0
fi
umask 077
tmp=$(mktemp .env.init.XXXXXX)
trap 'rm -f "$tmp"' EXIT HUP INT TERM
while IFS= read -r line; do
  key=${line%%=*}
  case "$key" in
    POSTGRES_PASSWORD|APP_DB_PASSWORD|N8N_DB_PASSWORD|N8N_ENCRYPTION_KEY|INTERNAL_API_TOKEN|N8N_HOOK_TOKEN)
      secret=$(openssl rand -hex 32)
      printf '%s=%s\n' "$key" "$secret" ;;
    *) printf '%s\n' "$line" ;;
  esac
done < .env.example > "$tmp"
ln "$tmp" .env
echo 'Created private .env (0600). External integrations remain disabled.'
