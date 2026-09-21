import http.client
import unittest
import urllib.error
from unittest.mock import patch

from client import DesktopClient, timeout_setting
from reply import UpstreamError


class ClientTests(unittest.TestCase):
    def setUp(self):
        self.client = DesktopClient.__new__(DesktopClient)
        self.client.cookies, self.client.params = {}, {}
        self.client.models = {'doubao-auto': {'id': 'doubao-auto', 'model_item_key': '9'}}
        self.client.selection = {'mode_id': '3', 'reasoning_effort': 5}
        self.client.selections = {'doubao-auto': {**self.client.selection,
                                                'model': {'model_item_key': '9'}}}
        self.client.read_timeout, self.client.generation_timeout = 120, 600

    def catalog_client(self, models):
        defaults = {'office': {'mode_id': '3', 'reasoning_effort': 5},
                    'chat': {'mode_id': '1', 'reasoning_effort': 3}}
        with patch('client.validate_context'), patch('client.read_catalog', return_value=(models, defaults)):
            return DesktopClient({'cookies': {}, 'params': {}})

    def test_chat_and_work_models_use_their_own_protocol_and_reasoning(self):
        client = self.catalog_client([
            {'model_item_key': '3', 'default_mode': '1', 'reasoning_effort_config': {'default_level': 4}},
            {'model_item_key': '5', 'default_mode': '3'},
            {'model_item_key': '9', 'default_mode': '3'}])
        for name, key, mode, agent, conversation, reasoning in [
                ('doubao-chat-turbo', '3', '1', 2, 1, 4),
                ('doubao-pro', '5', '3', 1, 2, 5), ('doubao-auto', '9', '3', 1, 2, 5)]:
            with self.subTest(model=name), patch('client.request') as send, patch('client.read_reply'):
                client.complete('test', name)
                payload = send.call_args.args[2]
                self.assertEqual(payload['option']['conversation_init_ext'],
                                 {'model_item_key': key, 'mode_id': mode, 'reasoning_effort': str(reasoning)})
                self.assertEqual(payload['option']['agent_mode'], agent)
                self.assertEqual(payload['option']['conversation_mode'], conversation)

    def test_catalog_never_substitutes_a_different_model_or_mode(self):
        client = self.catalog_client([{'model_item_key': '3', 'default_mode': '3'},
                                      {'model_item_key': '5', 'default_mode': '1'}])
        self.assertEqual(client.models, {})
        for name in ('doubao-chat-turbo', 'doubao-pro'):
            with self.assertRaises(ValueError):
                client.resolve_model(name)

    def test_generation_uses_longer_network_and_stream_budgets(self):
        with patch('client.request') as request, patch('client.read_reply') as read:
            self.client.complete('test', 'doubao-auto')
            self.assertEqual(request.call_args.kwargs['timeout'], 120)
            self.assertEqual(read.call_args.kwargs['max_seconds'], 600)

    def test_transport_failures_are_not_generic_or_silently_disconnected(self):
        for error, code, status in [
                (TimeoutError(), 'upstream_timeout', 504),
                (urllib.error.URLError(TimeoutError()), 'upstream_timeout', 504),
                (ConnectionResetError(), 'upstream_connection_failed', 502),
                (urllib.error.URLError('dns'), 'upstream_connection_failed', 502),
                (http.client.IncompleteRead(b'partial'), 'upstream_connection_failed', 502)]:
            for stage in ('client.request', 'client.read_reply'):
                with self.subTest(error=type(error).__name__, stage=stage):
                    with patch('client.request'), patch(stage, side_effect=error):
                        with self.assertRaises(UpstreamError) as caught:
                            self.client.complete('test', 'doubao-auto')
                    self.assertEqual((caught.exception.code, caught.exception.status), (code, status))
                    self.assertIs(caught.exception.__cause__, error)

    def test_timeout_settings_reject_unbounded_values(self):
        for value in ('0', '-1', 'nan', 'inf', '1801'):
            with patch.dict('os.environ', {'TEST_DESKTOP_TIMEOUT': value}):
                with self.assertRaises(ValueError):
                    timeout_setting('TEST_DESKTOP_TIMEOUT', 120)
