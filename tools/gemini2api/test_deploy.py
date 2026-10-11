import json
import pathlib
import tempfile
import unittest
from unittest import mock
import deploy


class Reply:
    def __init__(self, payload):
        self.payload = json.dumps(payload).encode()

    def read(self, *_args):
        return self.payload

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False


class DeployTest(unittest.TestCase):
    def run_deploy(self, failure=False):
        original = {'platform': 'openai', 'type': 'apikey', 'proxy_id': 3,
                    'credentials': {'base_url': 'https://generativelanguage.googleapis.com/v1beta/openai',
                                    'api_key': 'google-secret', 'model_mapping': {'gemini-test': 'gemini-test'}},
                    'extra': {'openai_responses_supported': False}}
        calls = []
        commands = []

        def query(sql):
            if sql.startswith('SELECT row_to_json'):
                return json.dumps(original)
            if 'admin_api_key' in sql:
                return 'admin-secret'
            if 'SELECT k.key' in sql:
                return 'group-secret'
            if 'MAX(id)' in sql:
                return '0'
            if 'usage_logs' in sql:
                return '885'
            raise AssertionError('Unexpected SQL')

        def command(args, **_kwargs):
            commands.append(args)
            return 'healthy' if '{{.State.Health.Status}}' in args else ''

        def open_request(request, **_kwargs):
            calls.append((request.full_url, json.loads(request.data)))
            if request.full_url.endswith('/v1/responses'):
                return Reply({'status': 'failed' if failure else 'completed', 'output': [{'content': [{'text': 'OK'}]}]})
            return Reply({'data': {}})

        opener = mock.Mock()
        opener.open.side_effect = open_request
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            with mock.patch.object(deploy, 'query', side_effect=query), \
                 mock.patch.object(deploy, 'command', side_effect=command), \
                 mock.patch.object(deploy.subprocess, 'check_call'), \
                 mock.patch.object(deploy.urllib.request, 'build_opener', return_value=opener), \
                 mock.patch('builtins.print'):
                if failure:
                    with self.assertRaises(RuntimeError):
                        deploy.deploy(885, 'a' * 40, root)
                    self.assertFalse((root / 'runtime.json').exists())
                else:
                    deploy.deploy(885, 'a' * 40, root)
                    runtime = json.loads((root / 'runtime.json').read_text())
                    self.assertEqual(runtime['gemini_key'], 'google-secret')
                    self.assertEqual((root / 'runtime.json').stat().st_mode & 0o777, 0o600)
        return original, calls, commands

    def test_healthy_candidate_switches_only_target_account(self):
        _original, calls, commands = self.run_deploy()
        switch = calls[0][1]
        self.assertTrue(calls[0][0].endswith('/accounts/885'))
        self.assertEqual(switch['proxy_id'], 0)
        self.assertEqual(switch['extra']['openai_responses_mode'], 'force_responses')
        self.assertNotEqual(switch['credentials']['api_key'], 'google-secret')
        self.assertFalse(any('stop' in command for command in commands))
        self.assertFalse(any('sub2api-sub2api-1' in command for command in commands))

    def test_failed_acceptance_restores_account_and_stops_only_candidate(self):
        original, calls, commands = self.run_deploy(failure=True)
        restore = calls[-1][1]
        self.assertEqual(restore['credentials'], original['credentials'])
        self.assertEqual(restore['proxy_id'], 3)
        stops = [command for command in commands if 'stop' in command]
        self.assertEqual(len(stops), 1)
        self.assertTrue(stops[0][-1].startswith('gemini-qa-'))


if __name__ == '__main__':
    unittest.main()
