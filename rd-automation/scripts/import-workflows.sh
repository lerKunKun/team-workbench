#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# First-time bootstrap. Never overwrite a configured owner's workflows on updates.
existing=$(docker compose exec -T postgres psql -U rd_admin -d rd_platform -Atc "SELECT count(*) FROM n8n.workflow_entity WHERE id IN ('RdV2Intake000001','RdV2Confirm00001','RdV1Daily0000001')")
if [ "$existing" != 0 ]; then
  echo 'RD workflows already exist. Skipping import; publish them in the UI if needed.'
  exit 0
fi
# Stream JSON directly into a 0600 container temp file; credentials are stored encrypted by n8n.
python3 - <<'PY' | docker compose exec -T n8n sh -eu -c '
umask 077
f=$(mktemp)
trap '\''rm -f "$f"'\'' EXIT HUP INT TERM
cat > "$f"
n8n import:credentials --input="$f"
'
import json, re
with open('.env', encoding='utf-8') as f:
    keys = dict(line.rstrip('\n').split('=', 1) for line in f if re.match(r'^(INTERNAL_API_TOKEN|N8N_HOOK_TOKEN)=', line))
def cred(id, name, header, key):
    value = keys[key]
    if not re.fullmatch(r'[a-fA-F0-9]{64}', value):
        raise SystemExit(f'{key}: expected a 64-character hex secret')
    return {'id': id, 'name': name, 'type': 'httpHeaderAuth', 'data': {'name': header, 'value': value}}
print(json.dumps([
    cred('RdWorkflowHeader', 'RD Gateway → n8n', 'X-Workflow-Token', 'N8N_HOOK_TOKEN'),
    cred('RdGatewayHeader1', 'RD n8n → Gateway', 'X-Internal-Token', 'INTERNAL_API_TOKEN'),
]))
PY
for file in RD-V2-DingTalk-AI-Intake RD-V2-DingTalk-Card-Confirm RD-V1-Daily-Digest RD-Workbench-DM-Intake RD-Workbench-DM-Action; do
  docker compose exec -T n8n n8n import:workflow --input="/workflows/$file.json"
done
docker compose exec -T n8n n8n publish:workflow --id=RdV2Intake000001
docker compose exec -T n8n n8n publish:workflow --id=RdV2Confirm00001
# Publishing via CLI while n8n runs does not refresh the main process's webhook routing.
docker compose restart n8n
docker compose up -d --wait --wait-timeout 120 postgres n8n
