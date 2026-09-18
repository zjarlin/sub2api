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
        self.client.read_timeout, self.client.generation_timeout = 120, 600

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
