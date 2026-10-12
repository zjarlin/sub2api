import http.client
import json
import socket
import tempfile
import threading
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

from server import Adapter, DEFAULT_MAX_REQUEST_BODY_BYTES, handler_for
from api import parse_request
from reply import UpstreamError
from test_structured_output import schema_format
from test_tool_calls import function_tool
from transport import TESTED_VERSION
from models import ConversationCursor


class ServerTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.session_file = Path(directory.name) / 'session.json'
        self.session_file.write_text('{}')
        self.key_file = Path(directory.name) / 'api_key'
        self.key_file.write_text('test-key' * 8)
        client_patch = patch('server.DesktopClient')
        self.client_factory = client_patch.start()
        self.client = self.client_factory.return_value
        self.addCleanup(client_patch.stop)
        log_patch = patch('server.print')
        self.logs = log_patch.start()
        self.addCleanup(log_patch.stop)
        self.client.resolve_model.return_value = {'id': 'doubao-pro'}
        self.client.complete.return_value = SimpleNamespace(text='verified reply')
        self.adapter = Adapter(self.session_file, self.key_file)
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), handler_for(self.adapter))
        self.addCleanup(self.server.server_close)
        worker = threading.Thread(target=self.server.serve_forever, kwargs={'poll_interval': 0.01})
        worker.start()
        self.addCleanup(worker.join)
        self.addCleanup(self.server.shutdown)

    def request(self, body=b'', headers=None, truncate=False, path='/v1/chat/completions', method='POST', authorized=True):
        connection = http.client.HTTPConnection(*self.server.server_address, timeout=5)
        self.addCleanup(connection.close)
        connection.putrequest(method, path)
        if authorized:
            connection.putheader('Authorization', 'Bearer ' + self.adapter.key)
        connection.putheader('Content-Type', 'application/json')
        for name, value in headers if headers is not None else [('Content-Length', str(len(body)))]:
            connection.putheader(name, value)
        connection.endheaders(body)
        if truncate:
            connection.sock.shutdown(socket.SHUT_WR)
        response = connection.getresponse()
        self.response_headers = dict(response.getheaders())
        return response.status, response.read()

    def payload(self, stream=False):
        return json.dumps({'model': 'doubao-pro', 'stream': stream,
                           'messages': [{'role': 'user', 'content': '正文🙂' * 10000}]},
                          ensure_ascii=False).encode()

    def test_large_json_and_stream_preserve_entire_prompt(self):
        self.assertEqual(self.adapter.max_request_body_bytes, 32 * 1024 * 1024)
        for stream in (False, True):
            with self.subTest(stream=stream):
                body = self.payload(stream)
                self.assertGreater(len(body), 65536)
                status, raw = self.request(body)
                self.assertEqual(status, 200)
                self.client.complete.assert_called_with('正文🙂' * 10000, 'doubao-pro')
                if stream:
                    self.assertTrue(raw.endswith(b'data: [DONE]\n\n'))
                    self.assertIn(b'verified reply', raw)
                else:
                    self.assertEqual(json.loads(raw)['choices'][0]['message']['content'], 'verified reply')

    def test_large_multi_turn_context_preserves_roles_and_content(self):
        messages = [{'role': 'system', 'content': '保留完整上下文'},
                    {'role': 'user', 'content': '历史' * 20000},
                    {'role': 'assistant', 'content': '已收到'},
                    {'role': 'user', 'content': '继续'}]
        body = json.dumps({'model': 'doubao-pro', 'messages': messages}, ensure_ascii=False).encode()
        self.assertGreater(len(body), 65536)
        self.assertEqual(self.request(body)[0], 200)
        text = self.client.complete.call_args.args[0]
        self.assertEqual(json.loads(text.split('\n', 1)[1]), messages)

    def test_continuation_tails_succeed_over_json_and_sse(self):
        for stream in (False, True):
            for role, content in (('assistant', 'Working on it'), ('assistant', ''),
                                  ('developer', 'Finish the task'), ('system', 'Finish the task'), ('user', '')):
                with self.subTest(stream=stream, role=role, content=content):
                    messages = [{'role': 'user', 'content': 'Complete the task'}, {'role': role, 'content': content}]
                    body = json.dumps({'model': 'doubao-pro', 'stream': stream, 'messages': messages}).encode()
                    status, raw = self.request(body)
                    self.assertEqual(status, 200)
                    self.assertIn(b'verified reply', raw)
                    if stream:
                        self.assertTrue(raw.endswith(b'data: [DONE]\n\n'))
                    text = self.client.complete.call_args.args[0]
                    self.assertEqual(json.loads(text.split('\n', 1)[1]), messages)

    def test_history_over_100_messages_succeeds_over_json_and_sse(self):
        messages = [{'role': 'system', 'content': 'Keep the entire history'}]
        for index in range(60):
            messages.extend([
                {'role': 'assistant', 'content': None, 'tool_calls': [
                    {'id': f'call_{index}', 'type': 'function',
                     'function': {'name': 'lookup', 'arguments': '{}'}}]},
                {'role': 'tool', 'tool_call_id': f'call_{index}', 'content': f'result_{index}'},
            ])
        for stream in (False, True):
            with self.subTest(stream=stream):
                body = json.dumps({'model': 'doubao-pro', 'stream': stream, 'messages': messages}).encode()
                status, raw = self.request(body)
                self.assertEqual(status, 200)
                text = self.client.complete.call_args.args[0]
                self.assertEqual(json.loads(text.split('\n', 1)[1]), messages)
                if stream:
                    self.assertTrue(raw.endswith(b'data: [DONE]\n\n'))
                else:
                    self.assertEqual(json.loads(raw)['choices'][0]['message']['content'], 'verified reply')

    def test_configured_limit_is_inclusive_and_counts_bytes(self):
        body = self.payload()
        self.adapter.max_request_body_bytes = len(body)
        self.assertEqual(self.request(body)[0], 200)
        self.client.complete.reset_mock()
        status, raw = self.request(headers=[('Content-Length', str(len(body) + 1))])
        self.assertEqual(status, 413)
        error = json.loads(raw)['error']
        self.assertEqual(error['code'], 'request_too_large')
        self.assertIn(str(len(body)), error['message'])
        self.client.complete.assert_not_called()
        self.assertEqual(self.request(body)[0], 200)

    def test_default_limit_rejects_before_reading_body(self):
        status, raw = self.request(headers=[('Content-Length', str(DEFAULT_MAX_REQUEST_BODY_BYTES + 1))])
        self.assertEqual(status, 413)
        self.assertEqual(json.loads(raw)['error']['type'], 'desktop_adapter_error')
        self.client.complete.assert_not_called()

    def test_invalid_framing_is_not_reported_as_oversize(self):
        for headers in ([], [('Content-Length', '0')], [('Content-Length', '-1')],
                        [('Content-Length', 'invalid')], [('Transfer-Encoding', 'chunked')],
                        [('Content-Length', '2'), ('Content-Length', '2')]):
            with self.subTest(headers=headers):
                status, raw = self.request(headers=headers)
                self.assertEqual(status, 400)
                self.assertEqual(json.loads(raw)['error']['code'], 'invalid_request')
                self.client.complete.assert_not_called()

    def test_truncated_body_is_rejected_before_generation(self):
        body = self.payload()
        status, raw = self.request(body, [('Content-Length', str(len(body) + 1))], truncate=True)
        self.assertEqual(status, 400)
        self.assertEqual(json.loads(raw)['error']['code'], 'invalid_request')
        self.client.complete.assert_not_called()

    def test_configured_limit_accepts_environment_string_and_rejects_invalid_values(self):
        adapter = Adapter(self.session_file, self.key_file, '1048576')
        self.assertEqual(adapter.max_request_body_bytes, 1048576)
        for value in ('0', '-1', 'invalid'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                Adapter(self.session_file, self.key_file, value)

    def test_named_profiles_load_separate_snapshots_and_state(self):
        profiles = self.session_file.parent / 'profiles'
        profiles.mkdir()
        (profiles / 'alice.json').write_text('{"profile":"alice"}')
        (profiles / 'bob.json').write_text('{"profile":"bob"}')
        self.adapter.reload('alice')
        alice = self.adapter.profile_states['alice']
        self.adapter.reload('bob')
        bob = self.adapter.profile_states['bob']
        self.assertIsNot(alice[1], bob[1])
        self.assertEqual(alice[1].path.name, 'alice.json')
        self.assertEqual(bob[1].path.name, 'bob.json')
        with self.assertRaises(ValueError):
            self.adapter.reload('../session')

    def snapshot(self, name='alice'):
        return json.dumps({'desktop_version': TESTED_VERSION, 'cookies': {'sessionid': name + '-secret'},
                           'params': {}}).encode()

    def test_profile_http_upload_list_replace_delete_and_private_storage(self):
        status, raw = self.request(self.snapshot(), path='/internal/profiles/alice')
        self.assertEqual(status, 201)
        self.assertEqual(json.loads(raw), {'profile': 'alice'})
        snapshot = self.adapter.profile_dir / 'alice.json'
        self.assertEqual(snapshot.stat().st_mode & 0o777, 0o600)
        self.assertEqual(snapshot.parent.stat().st_mode & 0o777, 0o700)
        self.adapter.reload('alice')
        state = self.adapter.conversations.path
        response_state = self.adapter.responses.path
        self.assertNotEqual(state.parent, snapshot.parent)
        self.assertTrue(state.exists())
        status, raw = self.request(path='/internal/profiles', method='GET')
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(raw), {'profiles': ['default', 'alice']})
        self.assertNotIn(b'secret', raw)
        self.assertEqual(self.request(self.snapshot('bob'), path='/internal/profiles/alice')[0], 201)
        self.assertFalse(state.exists())
        self.assertFalse(response_state.exists())
        self.adapter.reload('alice')
        self.assertTrue(state.exists())
        self.assertEqual(self.request(path='/internal/profiles/alice', method='DELETE')[0], 200)
        self.assertFalse(snapshot.exists())
        self.assertFalse(state.exists())
        self.assertFalse(response_state.exists())
        self.assertEqual(self.adapter.list_profiles(), ['default'])
        self.assertNotIn('alice', self.adapter.profile_states)
        with self.assertRaises(ValueError):
            self.adapter.reload('alice')

    def test_invalid_snapshot_cannot_replace_an_existing_profile(self):
        self.adapter.save_profile('alice', self.snapshot())
        path = self.adapter.profile_dir / 'alice.json'
        for raw in (b'{}', b'[]', b'invalid', b'\xff', b'x' * 65537,
                    json.dumps({'desktop_version': 'unsupported', 'cookies': {'sessionid': 's'}, 'params': {}}).encode(),
                    json.dumps({'desktop_version': TESTED_VERSION, 'cookies': {'sessionid': 's'}, 'params': {'a_bogus': 'x'}}).encode()):
            with self.subTest(raw=raw[:25]):
                self.assertEqual(self.request(raw, path='/internal/profiles/alice')[0], 400)
                self.assertEqual(path.read_bytes(), self.snapshot())

    def test_profile_management_requires_authentication_and_rejects_unsafe_names(self):
        for method, path in [('GET', '/internal/profiles'), ('POST', '/internal/profiles/alice'),
                             ('DELETE', '/internal/profiles/alice')]:
            self.assertEqual(self.request(self.snapshot(), method=method, path=path, authorized=False)[0], 401)
        for name in ('default', '../session', '%2e%2e', '_alice', 'a' * 65):
            for method in ('POST', 'DELETE'):
                self.assertGreaterEqual(self.request(self.snapshot(), method=method, path='/internal/profiles/' + name)[0], 400)
        self.assertEqual(self.session_file.read_text(), '{}')
        self.assertEqual(self.adapter.list_profiles(), ['default'])

    def test_profile_upload_rejects_invalid_framing_and_busy_adapter(self):
        for headers in ([], [('Content-Length', '0')], [('Content-Length', '65537')],
                        [('Content-Length', '2'), ('Content-Length', '2')],
                        [('Content-Length', '2'), ('Transfer-Encoding', 'chunked')]):
            self.assertEqual(self.request(headers=headers, path='/internal/profiles/alice')[0], 400)
        self.assertEqual(self.request(b'{}', [('Content-Length', '3')], truncate=True,
                                      path='/internal/profiles/alice')[0], 400)
        with self.adapter.lock:
            self.assertEqual(self.request(self.snapshot(), path='/internal/profiles/alice')[0], 429)
        self.assertEqual(self.request(self.snapshot(), path='/internal/profiles/alice')[0], 201)

    def test_requests_use_selected_profile_and_isolate_verification_failures(self):
        for name in ('alice', 'bob'):
            self.adapter.save_profile(name, self.snapshot(name))
        clients = {}
        def create_client(context):
            name = context['cookies']['sessionid']
            client = Mock()
            client.resolve_model.return_value = {'id': 'doubao-pro'}
            client.complete.return_value = SimpleNamespace(text=name)
            clients[name] = client
            return client
        self.client_factory.side_effect = create_client
        body = json.dumps({'model': 'doubao-pro', 'messages': [{'role': 'user', 'content': 'hello'}]}).encode()
        def generate(name):
            return self.request(body, [('Content-Length', str(len(body))), ('X-Desktop-Profile', name)])
        self.assertIn(b'alice-secret', generate('alice')[1])
        clients['alice-secret'].complete.side_effect = UpstreamError(710022004, 503)
        self.assertEqual(generate('alice')[0], 503)
        self.assertIn(b'bob-secret', generate('bob')[1])
        self.assertEqual(generate('alice')[0], 503)
        self.assertEqual(clients['alice-secret'].complete.call_count, 2)
        self.assertEqual(generate('missing')[0], 502)
        self.assertIn(b'bob-secret', generate('bob')[1])
        self.assertFalse(self.adapter.blocked)

    def test_profile_models_and_repeated_first_turn_without_scope(self):
        self.adapter.save_profile('alice', self.snapshot())
        self.client.models = {'doubao-pro': {'id': 'doubao-pro', 'name': 'Pro'}}
        status, raw = self.request(headers=[('X-Desktop-Profile', 'alice')], path='/v1/models', method='GET')
        self.assertEqual(status, 200)
        self.assertEqual(self.adapter.loaded_profile, 'alice')
        self.assertEqual(json.loads(raw)['data'][0]['id'], 'doubao-pro')
        self.client.complete.return_value = SimpleNamespace(
            text='hello', cursor=ConversationCursor('conv', 'section', 1))
        body = json.dumps({'model': 'doubao-pro', 'messages': [{'role': 'user', 'content': 'hello'}]}).encode()
        for _ in range(2):
            self.assertEqual(self.request(body, [('Content-Length', str(len(body))), ('X-Desktop-Profile', 'alice')])[0], 200)
        self.assertEqual(self.client.complete.call_count, 2)

    def test_failed_profile_reload_does_not_replace_current_client(self):
        self.adapter.save_profile('alice', self.snapshot())
        self.client_factory.side_effect = RuntimeError('catalog unavailable')
        with self.assertRaises(RuntimeError):
            self.adapter.reload('alice')
        self.assertIs(self.adapter.client, self.client)
        self.assertEqual(self.adapter.loaded_profile, 'default')

    def test_unknown_model_remains_a_client_error_after_selecting_profile(self):
        self.adapter.save_profile('alice', self.snapshot())
        self.client.resolve_model.side_effect = ValueError('Unknown desktop model; use /v1/models')
        body = json.dumps({'model': 'unknown', 'messages': [{'role': 'user', 'content': 'hello'}]}).encode()
        status, raw = self.request(body, [('Content-Length', str(len(body))), ('X-Desktop-Profile', 'alice')])
        self.assertEqual(status, 400)
        self.assertEqual(json.loads(raw)['error']['code'], 'invalid_request')
        self.client.complete.assert_not_called()

    def test_responses_history_survives_switching_profiles_but_never_crosses_accounts(self):
        for name in ('alice', 'bob'):
            self.adapter.save_profile(name, self.snapshot(name))
        def generate(profile, payload):
            body = json.dumps(payload).encode()
            return self.request(body, [('Content-Length', str(len(body))), ('X-Desktop-Profile', profile)],
                                path='/v1/responses')
        first = {'model': 'doubao-pro', 'input': 'hello'}
        status, raw = generate('alice', first)
        self.assertEqual(status, 200)
        previous = json.loads(raw)['id']
        continuation = {**first, 'input': 'continue', 'previous_response_id': previous}
        self.assertEqual(generate('bob', continuation)[0], 400)
        self.assertEqual(generate('bob', first)[0], 200)
        self.assertEqual(generate('alice', continuation)[0], 200)
        # 进程重启后仍可续聊，但仅限持有该响应的账号。
        self.adapter.profile_states.clear()
        self.adapter.profile_versions.clear()
        self.assertEqual(generate('alice', continuation)[0], 200)
        self.assertEqual(generate('bob', continuation)[0], 400)

    def test_capacity_cooldown_does_not_block_other_profiles(self):
        for name in ('alice', 'bob'):
            self.adapter.save_profile(name, self.snapshot(name))
        body = json.dumps({'model': 'doubao-pro', 'messages': [{'role': 'user', 'content': 'hello'}]}).encode()
        def generate(profile):
            return self.request(body, [('Content-Length', str(len(body))), ('X-Desktop-Profile', profile)])
        self.client.complete.side_effect = [UpstreamError('upstream_capacity', 503), SimpleNamespace(text='ready')]
        self.assertEqual(generate('alice')[0], 503)
        self.assertEqual(generate('bob')[0], 200)
        self.assertEqual(generate('alice')[0], 503)
        self.assertEqual(self.client.complete.call_count, 2)

    def test_response_formats_work_for_json_and_sse(self):
        formats = [{'type': 'text'}, {'type': 'json_object'}, schema_format(strict=True)]
        for output_format in formats:
            for stream in (False, True):
                with self.subTest(output_format=output_format['type'], stream=stream):
                    self.client.complete.return_value = SimpleNamespace(text='{"answer":"ok","count":2}')
                    body = json.dumps({'model': 'doubao-pro', 'stream': stream,
                                       'response_format': output_format,
                                       'messages': [{'role': 'user', 'content': 'Respond'}]}).encode()
                    status, raw = self.request(body)
                    self.assertEqual(status, 200)
                    if stream:
                        self.assertTrue(raw.endswith(b'data: [DONE]\n\n'))
                        event = json.loads(raw.decode().splitlines()[0][6:])
                        content = event['choices'][0]['delta']['content']
                    else:
                        content = json.loads(raw)['choices'][0]['message']['content']
                    self.assertEqual(json.loads(content), {'answer': 'ok', 'count': 2})

    def test_invalid_formatted_reply_never_emits_success_or_sse_chunks(self):
        self.client.complete.return_value = SimpleNamespace(text='unstructured reply')
        for stream in (False, True):
            body = json.dumps({'model': 'doubao-pro', 'stream': stream,
                               'response_format': {'type': 'json_object'},
                               'messages': [{'role': 'user', 'content': 'Respond'}]}).encode()
            status, raw = self.request(body)
            self.assertEqual(status, 502)
            self.assertEqual(json.loads(raw)['error']['code'], 'invalid_response_format')
            self.assertNotIn(b'data:', raw)

    def test_invalid_response_format_never_calls_upstream(self):
        body = json.dumps({'model': 'doubao-pro', 'response_format': {'type': 'yaml'},
                           'messages': [{'role': 'user', 'content': 'Respond'}]}).encode()
        status, raw = self.request(body)
        self.assertEqual(status, 400)
        self.assertEqual(json.loads(raw)['error']['code'], 'invalid_request')
        self.client.complete.assert_not_called()

    def test_tools_round_trip_over_json_and_sse(self):
        for stream in (False, True):
            self.client.complete.return_value = SimpleNamespace(text=json.dumps({
                'content': None, 'tool_calls': [{'name': 'lookup', 'arguments': {'query': 'alpha'}}]}))
            body = {'model': 'doubao-pro', 'stream': stream, 'tools': [function_tool()],
                    'tool_choice': 'required', 'parallel_tool_calls': False,
                    'messages': [{'role': 'user', 'content': 'Look up alpha'}]}
            status, raw = self.request(json.dumps(body).encode())
            self.assertEqual(status, 200)
            if stream:
                events = [json.loads(line[6:]) for line in raw.decode().splitlines() if line.startswith('data: {')]
                message = events[0]['choices'][0]['delta']
                self.assertEqual(events[-1]['choices'][0]['finish_reason'], 'tool_calls')
                message['tool_calls'][0].pop('index')
            else:
                choice = json.loads(raw)['choices'][0]
                self.assertEqual(choice['finish_reason'], 'tool_calls')
                message = choice['message']
            identifier = message['tool_calls'][0]['id']
            body['tool_choice'] = 'auto'
            body['messages'].extend([message, {'role': 'tool', 'tool_call_id': identifier, 'content': 'TOOL_RESULT'}])
            self.client.complete.return_value = SimpleNamespace(text='{"content":"TOOL_RESULT","tool_calls":[]}')
            status, raw = self.request(json.dumps(body).encode())
            self.assertEqual(status, 200)
            self.assertIn(b'TOOL_RESULT', raw)
            self.assertIn('TOOL_RESULT', self.client.complete.call_args.args[0])

    def test_bad_tool_reply_is_an_upstream_error_before_sse_success(self):
        self.client.complete.return_value = SimpleNamespace(text='{"content":"invented result","tool_calls":[]}')
        body = {'model': 'doubao-pro', 'stream': True, 'tools': [function_tool()], 'tool_choice': 'required',
                'messages': [{'role': 'user', 'content': 'Look up alpha'}]}
        status, raw = self.request(json.dumps(body).encode())
        self.assertEqual(status, 502)
        self.assertEqual(json.loads(raw)['error']['code'], 'invalid_tool_calls')
        self.assertNotIn(b'data:', raw)

    def test_capacity_error_cools_only_the_busy_model_and_does_not_commit(self):
        self.client.resolve_model.side_effect = lambda model: {'id': model}
        self.client.complete.side_effect = [UpstreamError('upstream_capacity', 503),
                                            SimpleNamespace(text='available'), SimpleNamespace(text='recovered')]
        request = parse_request({'model': 'doubao-auto', 'messages': [{'role': 'user', 'content': 'test'}]})
        with patch('server.time.monotonic', return_value=100):
            for _ in range(2):
                with self.assertRaisesRegex(UpstreamError, 'upstream_capacity'):
                    self.adapter.complete(request)
            self.assertEqual(self.client.complete.call_count, 1)
            self.assertFalse(self.adapter.conversations.entries)
            pro = parse_request({'model': 'doubao-pro', 'messages': request.messages})
            self.assertEqual(self.adapter.complete(pro)['choices'][0]['message']['content'], 'available')
        with patch('server.time.monotonic', return_value=131):
            self.assertEqual(self.adapter.complete(request)['choices'][0]['message']['content'], 'recovered')

    def test_upstream_failure_is_specific_and_diagnostics_do_not_leak_content(self):
        for code, status in [('upstream_capacity', 503), ('upstream_timeout', 504),
                             ('upstream_connection_failed', 502)]:
            self.adapter.capacity_until.clear()
            self.client.complete.side_effect = UpstreamError(code, status)
            actual, raw = self.request(self.payload())
            self.assertEqual(actual, status)
            self.assertEqual(json.loads(raw)['error']['code'], code)
            self.assertRegex(self.response_headers['X-Request-ID'], r'^[a-f0-9]{32}$')
            if code == 'upstream_capacity':
                self.assertEqual(self.response_headers['Retry-After'], '30')
        secret = 'private-cookie-and-prompt'
        self.client.complete.side_effect = RuntimeError(secret)
        actual, raw = self.request(self.payload())
        self.assertEqual(actual, 502)
        self.assertNotIn(secret, raw.decode())
        records = ''.join(str(call) for call in self.logs.call_args_list)
        self.assertNotIn(secret, records)
        self.assertNotIn(self.adapter.key, records)
        self.assertIn('upstream_timeout', records)
