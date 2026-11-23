#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
umask 077
mkdir -p backups
target=$(mktemp -d "backups/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")
docker compose exec -T postgres pg_dump -U rd_admin -d rd_platform -Fc > "$target/rd_platform.dump"
cp .env "$target/.env"
docker compose exec -T n8n tar -C /home/node/.n8n -czf - . > "$target/n8n-data.tgz"
echo "Backup written to $target. Contains secrets; encrypt before copying off-host."
