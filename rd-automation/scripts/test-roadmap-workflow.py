import json
import unittest
from pathlib import Path

workflows = Path(__file__).resolve().parents[1] / 'n8n' / 'workflows'


class WorkflowTest(unittest.TestCase):
    def test_combined_keeps_legacy_contracts(self):
        combined = json.loads((workflows / 'RD-Project-Workbench.json').read_text())
        nodes = {n['name']: n for n in combined['nodes']}
        hooks = {n['parameters']['path']: n for n in nodes.values() if n['type'].endswith('.webhook')}
        self.assertEqual(set(hooks), {'workbench-private-intake', 'workbench-private-action', 'dingtalk-ai-intake', 'dingtalk-card-action', 'roadmap-feedback'})
        self.assertEqual(len(set(n['id'] for n in nodes.values())), len(combined['nodes']))
        self.assertFalse(combined['active'])
        for name in ('RD-Workbench-DM-Intake', 'RD-Workbench-DM-Action', 'RD-V2-DingTalk-AI-Intake', 'RD-V2-DingTalk-Card-Confirm'):
            old_hook, old_forward = json.loads((workflows / f'{name}.json').read_text())['nodes']
            hook = hooks[old_hook['parameters']['path']]
            forward = nodes[combined['connections'][hook['name']]['main'][0][0]['node']]
            self.assertEqual(old_hook['parameters'], hook['parameters'])
            self.assertEqual(old_hook['credentials'], hook['credentials'])
            self.assertEqual(old_forward['parameters'], forward['parameters'])
            self.assertEqual(old_forward['credentials'], forward['credentials'])
        hook = hooks['roadmap-feedback']
        forward = nodes[combined['connections'][hook['name']]['main'][0][0]['node']]
        response = nodes[combined['connections'][forward['name']]['main'][0][0]['node']]
        self.assertEqual('RdRoadmapHook001', hook['credentials']['httpHeaderAuth']['id'])
        self.assertEqual('RdRoadmapImport1', forward['credentials']['httpHeaderAuth']['id'])
        self.assertEqual('http://team-workbench-api:8091/api/integrations/roadmap/feedback', forward['parameters']['url'])
        self.assertTrue(forward['parameters']['options']['response']['response']['fullResponse'])
        self.assertTrue(forward['parameters']['options']['response']['response']['neverError'])
        self.assertEqual('={{ $json.statusCode }}', response['parameters']['options']['responseCode'])


if __name__ == '__main__':
    unittest.main()
