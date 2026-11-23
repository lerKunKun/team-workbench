import copy
import json
from pathlib import Path

directory = Path(__file__).resolve().parents[1] / 'n8n' / 'workflows'
legacy = ('RD-Workbench-DM-Intake', 'RD-Workbench-DM-Action', 'RD-V2-DingTalk-AI-Intake', 'RD-V2-DingTalk-Card-Confirm')
workflow = {'id': 'RdProjectWorkbench', 'name': 'RD-项目工作台', 'active': False, 'nodes': [], 'connections': {}}


def link(left, right):
    workflow['connections'][left['name']] = {'main': [[{'node': right['name'], 'type': 'main', 'index': 0}]]}


for row, name in enumerate(legacy):
    original = json.loads((directory / f'{name}.json').read_text())
    hook, send = original['nodes']
    hook['name'] = hook['parameters']['path']
    for node in (hook, send):
        node['position'][1] = row * 240
    workflow['nodes'].extend((hook, send))
    link(hook, send)
    workflow['settings'] = original['settings']

hook = copy.deepcopy(workflow['nodes'][0])
hook.update(id='08449921-b36d-4d40-9b18-b5fcf7179010', webhookId='0be3b5ec-a290-402a-a240-4fa88fc34a3e', name='roadmap-feedback', position=[0, 960])
hook['parameters'].update(path='roadmap-feedback', responseMode='responseNode')
hook['credentials']['httpHeaderAuth'] = {'id': 'RdRoadmapHook001', 'name': 'RD Workbench → Roadmap webhook'}
del hook['parameters']['responseData']
send = copy.deepcopy(workflow['nodes'][1])
send.update(id='f0e90aa9-abbf-4c46-997d-1decb79b4d77', name='Import approved Roadmap item', position=[280, 960])
send['parameters'].update(url='http://team-workbench-api:8091/api/integrations/roadmap/feedback', jsonBody='={{ JSON.stringify({ itemId: $json.body.itemId, projectId: $json.body.projectId, title: $json.body.title, summary: $json.body.summary, votes: $json.body.votes }) }}', options={'timeout': 8000, 'redirect': {'redirect': {'followRedirects': False}}, 'response': {'response': {'fullResponse': True, 'neverError': True, 'responseFormat': 'json'}}})
send['credentials']['httpHeaderAuth'] = {'id': 'RdRoadmapImport1', 'name': 'RD n8n → Roadmap import'}
response = {'id': '4f60a601-d12e-4b68-a228-7cff9d38a35e', 'name': 'Return import status', 'type': 'n8n-nodes-base.respondToWebhook', 'typeVersion': 1.4, 'position': [560, 960], 'parameters': {'respondWith': 'json', 'responseBody': '={{ $json.statusCode === 200 ? { id: $json.body.id } : { ok: false } }}', 'options': {'responseCode': '={{ $json.statusCode }}'}}}
workflow['nodes'].extend((hook, send, response))
link(hook, send)
link(send, response)
(directory / 'RD-Project-Workbench.json').write_text(json.dumps(workflow, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
