"""离线 HTTP 验收：合成设备流、真实适配器入口与同一生成的结果续接。"""
import copy
import http.client
import io
import json
import queue
import tempfile
import threading
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

from native_client import NativeClient, DEVICE_STREAM, TOOL_RESULT, COMMAND_RESULT
from reply import UpstreamError
from responses import parse_request
from server import Adapter, handler_for


FUNCTION = {'type': 'function', 'name': 'lookup', 'description': 'Return a marker',
            'parameters': {'type': 'object', 'properties': {'key': {'type': 'string'}},
                           'required': ['key'], 'additionalProperties': False}}
MODELS = [{'model_item_key': key, 'name': key, 'default_mode': mode}
          for key, mode in [('5', '3'), ('9', '3'), ('3', '1')]]
DEFAULTS = {'office': {'mode_id': '3', 'reasoning_effort': 5},
            'chat': {'mode_id': '1', 'reasoning_effort': 3}}


def context():
    tool = {'name': 'lookup', 'description': FUNCTION['description'],
            'inputSchema': FUNCTION['parameters'], 'annotations': {'readOnlyHint': True}}
    return {'desktop_version': '2.31.4', 'cookies': {'sessionid': 'offline-only'},
            'params': {'device_id': '123'}, 'native_tools': {
                'connector_id': 'connector-a',
                'client_option': {'client_env_id': 'env-a', 'sandbox_id': 'sandbox-a',
                                  'workspace': '/offline', 'os': 'darwin', 'enable_sandbox': False},
                'connector_runtime': {'connectorId': 'connector-a', 'status': 'READY', 'enabled': True},
                'mcp_catalog': {'isOK': True, 'data': {'connectors': [
                    {'connectorId': 'connector-a', 'toolsJson': json.dumps([tool])}]}}}}


def call_frame(identifier='call-a', arguments=None):
    envelope = {'protocol_version': 1, 'connector_id': 'connector-a', 'remote_tool_name': 'lookup',
                'raw_arguments': arguments if arguments is not None else '{"key":"native-proof"}'}
    return {'event_type': 1, 'sandbox_id': 'sandbox-a', 'tool_call': {
        'id': identifier, 'name': 'connector.call', 'input': json.dumps(envelope)}}


def command_frame(kind=1, ack=False):
    action = {1: 'connect', 2: 'disconnect', 3: 'release'}[kind]
    return {'event_type': 2, 'sandbox_id': 'sandbox-a', 'command': {
        'id': 'command-a', 'name': 'sandbox', 'need_ack': ack,
        'input': json.dumps({'type': kind, action: {'sandbox_id': 'sandbox-a', 'id': 'connection-b'}})}}


class EventStream:
    def __init__(self):
        self.status = 200
        self.headers = {'Content-Type': 'text/event-stream'}
        self.lines = queue.Queue()
        self.closed = threading.Event()

    def emit(self, frame, event='message'):
        for line in ('event: ' + event + '\n', 'data: ' + json.dumps(frame) + '\n', '\n'):
            self.lines.put(line.encode())

    def readline(self, size):
        if self.closed.is_set():
            return b''
        return self.lines.get(timeout=3)

    def close(self):
        self.closed.set()
        self.lines.put(b'')


class Upstream:
    def __init__(self):
        self.device, self.generation = EventStream(), EventStream()
        self.requests = []
        self.device.emit({'event_type': 3})
        self.frames = [call_frame()]
        self.upload_hook = None
        self.ack_error = False
        self.finish_without_tools = False

    def send(self, path, payload, **kwargs):
        self.requests.append((path, copy.deepcopy(payload), kwargs))
        if path == DEVICE_STREAM:
            return self.device
        if path == '/chat/completion':
            extra = {**payload['option']['conversation_init_ext'], 'inner_app_id': '582478'}
            ack = {'ack_client_meta': {'conversation_id': '123456', 'conversation_type': 3,
                                      'local_conversation_id': payload['client_meta']['local_conversation_id'],
                                      'conversation_info': {'extra': extra}},
                   'query_list': [{'message_index': 1}]}
            if self.ack_error:
                extra['model_item_key'] = 'wrong-model'
            self.generation.emit(ack, 'SSE_ACK')
            self.generation.emit({'meta': {'user_type': 2, 'conversation_id': '123456', 'message_id': '789'},
                                  'content': {'content_block': []}}, 'STREAM_MSG_NOTIFY')
            for frame in self.frames:
                self.device.emit(frame)
            if self.finish_without_tools:
                self.finish('{"calls":[{"name":"lookup","arguments":{}}]}')
            return self.generation
        if path == TOOL_RESULT:
            if self.upload_hook:
                self.upload_hook(payload)
            else:
                self.finish('真实回传结果: marker-123')
        response = io.BytesIO(b'{"code":0}')
        response.status = 200
        return response

    def finish(self, text):
        self.generation.emit({'message_id': '789', 'patch_op': [{'patch_object': 1, 'patch_value': {
            'content_block': [{'block_id': 'answer', 'block_type': 10000, 'patch_type': 2,
                               'content': {'text_block': {'text': text}}}]}}]}, 'STREAM_CHUNK')
        self.generation.emit({'end_type': 1, 'msg_finish_attr': {'msgid': '789'}}, 'SSE_REPLY_END')
        self.generation.emit({'end_type': 3}, 'SSE_REPLY_END')

    def count(self, path):
        return sum(item[0] == path for item in self.requests)


class NativeFixture(unittest.TestCase):
    def setUp(self):
        self.upstream = Upstream()
        self.client = NativeClient(context(), send=self.upstream.send,
                                   catalog_reader=lambda *args, **kwargs: (MODELS, DEFAULTS))
        self.client.connect_timeout = 0.1
        self.client.generation_timeout = 2
        self.addCleanup(self.client.close)

    def first(self, **changes):
        return parse_request({'model': 'doubao-pro', 'input': '请查标记', 'tools': [FUNCTION], **changes})

    def continuation(self, first, **changes):
        return parse_request({'model': 'doubao-pro', 'previous_response_id': first['id'],
                              'input': [{'type': 'function_call_output', 'call_id': 'call-a',
                                         'output': 'marker-123'}], **changes})


class NativeClientTests(NativeFixture):
    def test_roundtrip_uses_one_generation_original_id_and_native_result(self):
        first = self.client.complete_response(self.first(), 'scope-a')
        self.assertEqual(first['output'][0]['call_id'], 'call-a')
        self.assertEqual(first['output'][0]['arguments'], '{"key":"native-proof"}')
        request = self.continuation(first)
        final = self.client.complete_response(request, 'scope-a')
        self.assertEqual(final['output'][0]['content'][0]['text'], '真实回传结果: marker-123')
        self.assertEqual(final['tools'], [FUNCTION])
        self.assertEqual(self.client.complete_response(request, 'scope-a'), final)
        self.assertEqual(self.upstream.count('/chat/completion'), 1)
        self.assertEqual(self.upstream.count(DEVICE_STREAM), 1)
        self.assertEqual(self.upstream.count(TOOL_RESULT), 1)
        payload = next(payload for path, payload, _ in self.upstream.requests if path == TOOL_RESULT)
        result = payload['tool_result']
        self.assertEqual(result['id'], 'call-a')
        self.assertEqual(result['tool_input'], call_frame()['tool_call']['input'])
        self.assertEqual(json.loads(result['raw_result']), {
            'content': [{'type': 'text', 'text': 'marker-123'}], 'isError': False})
        generation = next(payload for path, payload, _ in self.upstream.requests if path == '/chat/completion')
        tool = json.loads(generation['option']['general_task_param']['task_input_json'])['localConnectors'][0]['mcpToolsList'][0]
        self.assertEqual(json.loads(tool['annotations']), {'readOnlyHint': True})
        prompt = generation['messages'][0]['content_block'][0]['content']['text_block']['text']
        for forbidden in ('"parameters"', '"calls"', 'Return a marker', '只返回一个 JSON 对象'):
            self.assertNotIn(forbidden, prompt)
        self.assertTrue(self.upstream.device.closed.wait(0.5))
        self.assertTrue(self.upstream.generation.closed.wait(0.5))

    def test_no_heartbeat_prevents_generation_and_does_not_retry(self):
        self.upstream.device = EventStream()
        with self.assertRaisesRegex(UpstreamError, 'native_device_not_ready'):
            self.client.complete_response(self.first())
        self.assertEqual(self.upstream.count('/chat/completion'), 0)
        self.assertEqual(self.upstream.count(DEVICE_STREAM), 1)
        self.assertTrue(self.upstream.device.closed.is_set())

    def test_heartbeat_without_id_and_connect_can_update_route(self):
        self.upstream.device.emit({'event_type': 3, 'heartbeat': {'connection_id': 'connection-a'}})
        self.upstream.frames = [command_frame(), {'event_type': 3}, call_frame(), call_frame()]
        first = self.client.complete_response(self.first())
        self.assertEqual(len(first['output']), 1)
        self.assertEqual(self.client.active.connection_id, 'connection-b')
        self.assertEqual(self.upstream.count(COMMAND_RESULT), 0)
        self.client.complete_response(self.continuation(first))

    def test_lifecycle_is_never_acknowledged_as_success(self):
        self.upstream.frames = [command_frame(ack=True)]
        with self.assertRaisesRegex(UpstreamError, 'native_lifecycle_unavailable'):
            self.client.complete_response(self.first())
        result = next(body for path, body, _ in self.upstream.requests if path == COMMAND_RESULT)
        self.assertEqual(result['command_result']['code'], 1)
        self.assertEqual(self.upstream.count(TOOL_RESULT), 0)

    def test_tool_frames_before_generation_are_rejected(self):
        self.upstream.device = EventStream()
        self.upstream.device.emit(call_frame())
        with self.assertRaisesRegex(UpstreamError, 'native_protocol_error'):
            self.client.complete_response(self.first())
        self.assertEqual(self.upstream.count('/chat/completion'), 0)

    def test_invalid_generation_ack_never_delivers_tools(self):
        self.upstream.ack_error = True
        with self.assertRaisesRegex(UpstreamError, 'desktop_conversation_isolation_failed'):
            self.client.complete_response(self.first())
        self.assertEqual(self.upstream.count(TOOL_RESULT), 0)

    def test_device_eof_invalid_scope_schema_or_agent_ends_run(self):
        for mutate in ('eof', 'sandbox', 'schema', 'agent'):
            with self.subTest(mutate=mutate):
                self.setUp()
                if mutate == 'eof':
                    self.upstream.device.close()
                elif mutate == 'sandbox':
                    self.upstream.frames[0]['sandbox_id'] = 'other'
                elif mutate == 'schema':
                    self.upstream.frames = [call_frame(arguments='{"key":123}')]
                else:
                    command = command_frame()
                    command['command']['option'] = {'agent_id': 'agent-a'}
                    self.upstream.frames = [command, call_frame()]
                with self.assertRaises(UpstreamError):
                    self.client.complete_response(self.first())
                self.assertEqual(self.upstream.count(TOOL_RESULT), 0)

    def test_invalid_contract_is_rejected_without_any_transport(self):
        for changes in ({'store': False}, {'model': 'doubao-chat-turbo'},
                        {'tools': [{**FUNCTION, 'parameters': {'type': 'object'}}]},
                        {'tools': [{**FUNCTION, 'namespace': 'ns'}]}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.client.complete_response(self.first(**changes))
        self.assertEqual(self.upstream.requests, [])

    def test_wrong_outputs_scope_model_and_policy_rejected_before_upload(self):
        first = self.client.complete_response(self.first(), 'scope-a')
        cases = [({'input': []}, 'scope-a'),
                 ({'input': [{'type': 'function_call_output', 'call_id': 'other', 'output': 'x'}]}, 'scope-a'),
                 ({'input': [{'type': 'function_call_output', 'call_id': 'call-a', 'output': 'x'}] * 2}, 'scope-a'),
                 ({'tools': []}, 'scope-a'), ({'instructions': 'changed'}, 'scope-a'),
                 ({'model': 'doubao-auto'}, 'scope-a'), ({'store': False}, 'scope-a'),
                 ({'tool_choice': 'none'}, 'scope-a'), ({}, 'scope-b')]
        for changes, scope in cases:
            with self.subTest(changes=changes, scope=scope), self.assertRaises(ValueError):
                self.client.validate_request(self.continuation(first, **changes), scope)
        self.assertEqual(self.upstream.count(TOOL_RESULT), 0)
        self.client.complete_response(self.continuation(first), 'scope-a')
        changed = self.continuation(first, input=[{'type': 'function_call_output', 'call_id': 'call-a', 'output': 'different'}])
        with self.assertRaisesRegex(ValueError, 'Conflicting replay'):
            self.client.complete_response(changed, 'scope-a')

    def test_upload_timeout_is_terminal_and_never_retried(self):
        first = self.client.complete_response(self.first())

        def failed_upload(payload):
            raise TimeoutError('uncertain')

        self.upstream.upload_hook = failed_upload
        request = self.continuation(first)
        for _ in range(2):
            with self.assertRaisesRegex(UpstreamError, 'native_upstream_timeout'):
                self.client.complete_response(request)
        self.assertEqual(self.upstream.count(TOOL_RESULT), 1)
        self.assertEqual(self.upstream.count('/chat/completion'), 1)

    def test_multiple_native_calls_continue_one_generation_and_partial_upload_is_terminal(self):
        first = self.client.complete_response(self.first(parallel_tool_calls=False))

        def upload(payload):
            if payload['tool_result']['id'] == 'call-a':
                self.upstream.device.emit(call_frame('call-b'))
                return
            raise TimeoutError('second upload outcome unknown')

        self.upstream.upload_hook = upload
        second = self.client.complete_response(self.continuation(first))
        self.assertEqual(second['output'][0]['call_id'], 'call-b')
        request = self.continuation(second, input=[{'type': 'function_call_output', 'call_id': 'call-b',
                                                    'output': [{'type': 'input_text', 'text': 'second'}]}])
        for _ in range(2):
            with self.assertRaisesRegex(UpstreamError, 'native_upstream_timeout'):
                self.client.complete_response(request)
        self.assertEqual(self.upstream.count('/chat/completion'), 1)
        self.assertEqual(self.upstream.count(TOOL_RESULT), 2)
        self.assertEqual(self.client.complete_response(self.continuation(first)), second)

    def test_final_generation_waits_for_result_upload_ack(self):
        first = self.client.complete_response(self.first())

        def upload(payload):
            self.upstream.finish('answer arrived before upload ack')
            # 模拟上游先完成生成，再给上传请求回执；等待期间仍有未确认上传。
            threading.Event().wait(0.02)
            with self.client.active.condition:
                self.assertIsNone(self.client.active.reply)

        self.upstream.upload_hook = upload
        final = self.client.complete_response(self.continuation(first))
        self.assertEqual(final['output'][0]['content'][0]['text'], 'answer arrived before upload ack')

    def test_not_ready_catalog_fails_before_model_catalog_or_transport(self):
        snapshot = context()
        snapshot['native_tools']['connector_runtime']['status'] = 'STARTING'
        with patch('native_client.read_catalog') as read_catalog:
            with self.assertRaises(ValueError):
                NativeClient(snapshot)
            read_catalog.assert_not_called()

    def test_pending_deadline_closes_streams_without_another_request(self):
        self.client.generation_timeout = 0.15
        first = self.client.complete_response(self.first())
        self.assertTrue(self.upstream.device.closed.wait(1))
        with self.assertRaisesRegex(UpstreamError, 'native_run_expired'):
            self.client.complete_response(self.continuation(first))
        self.assertEqual(self.upstream.count(TOOL_RESULT), 0)

    def test_final_text_is_never_interpreted_as_tool_calls(self):
        self.upstream.frames = []
        self.upstream.finish_without_tools = True
        result = self.client.complete_response(self.first())
        self.assertEqual(result['output'][0]['type'], 'message')
        self.assertIn('"calls"', result['output'][0]['content'][0]['text'])
        self.assertEqual(self.upstream.count(TOOL_RESULT), 0)

    def test_required_tool_missing_fails_and_store_false_is_not_retained(self):
        self.upstream.frames = []
        self.upstream.finish_without_tools = True
        with self.assertRaisesRegex(UpstreamError, 'native_required_tool_missing'):
            self.client.complete_response(self.first(tool_choice='required'))
        self.setUp()
        self.upstream.frames = []
        self.upstream.finish_without_tools = True
        self.client.complete_response(self.first(tools=[], store=False))
        self.assertEqual(self.client.responses, {})


class NativeHTTPTests(NativeFixture):
    def setUp(self):
        super().setUp()
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.session_file = self.root / 'session.json'
        self.session_file.write_text(json.dumps(context()))
        key_file = self.root / 'api_key'
        key_file.write_text('offline-key' * 8)
        factory = patch('server.NativeClient', return_value=self.client)
        factory.start()
        self.addCleanup(factory.stop)
        logs = patch('server.print')
        logs.start()
        self.addCleanup(logs.stop)
        self.adapter = Adapter(self.session_file, key_file)
        self.addCleanup(self.adapter.close)
        self.httpd = ThreadingHTTPServer(('127.0.0.1', 0), handler_for(self.adapter))
        self.addCleanup(self.httpd.server_close)
        worker = threading.Thread(target=self.httpd.serve_forever, kwargs={'poll_interval': 0.01})
        worker.start()
        self.addCleanup(worker.join)
        self.addCleanup(self.httpd.shutdown)

    def request(self, body, path='/v1/responses', method='POST', headers=None):
        connection = http.client.HTTPConnection(*self.httpd.server_address, timeout=4)
        try:
            connection.request(method, path, json.dumps(body), headers={
                'Authorization': 'Bearer ' + self.adapter.key, 'Content-Type': 'application/json', **(headers or {})})
            response = connection.getresponse()
            return response.status, response.read()
        finally:
            connection.close()

    def test_http_and_sse_roundtrip_bypass_every_text_bridge(self):
        with patch('responses.prompt', side_effect=AssertionError('text prompt used')), \
                patch('responses.decode', side_effect=AssertionError('text decoder used')), \
                patch.object(self.adapter.responses, 'plan', side_effect=AssertionError('legacy plan used')):
            status, raw = self.request({'model': 'doubao-pro', 'input': '查标记', 'tools': [FUNCTION], 'stream': True})
            self.assertEqual(status, 200, raw)
            events = [json.loads(line[6:]) for line in raw.decode().splitlines() if line.startswith('data: {')]
            first = next(event['response'] for event in events if event['type'] == 'response.completed')
            self.assertEqual(first['output'][0]['call_id'], 'call-a')
            self.assertTrue(any(event['type'] == 'response.function_call_arguments.done' for event in events))
            bad = {'model': 'doubao-pro', 'previous_response_id': first['id'],
                   'input': [{'type': 'function_call_output', 'call_id': 'wrong', 'output': 'x'}]}
            self.assertEqual(self.request(bad)[0], 400)
            self.assertEqual(self.upstream.count(TOOL_RESULT), 0)
            bad['input'][0].update(call_id='call-a', output='marker-123')
            status, raw = self.request(bad)
            self.assertEqual(status, 200, raw)
            self.assertEqual(json.loads(raw)['output'][0]['type'], 'message')
            self.assertEqual(self.upstream.count('/chat/completion'), 1)

    def test_chat_rejected_and_catalog_read_does_not_start_device_stream(self):
        status, raw = self.request({'model': 'doubao-pro', 'messages': [{'role': 'user', 'content': 'hi'}]},
                                   path='/v1/chat/completions')
        self.assertEqual(status, 400, raw)
        status, raw = self.request({}, path='/v1/models', method='GET')
        self.assertEqual(status, 200)
        self.assertEqual({item['id'] for item in json.loads(raw)['data']}, {'doubao-auto', 'doubao-pro'})
        self.assertEqual(self.upstream.requests, [])

    def test_profile_replacement_and_deletion_close_old_native_streams(self):
        self.adapter.save_profile('native', json.dumps(context()).encode())
        self.adapter.reload('native')
        self.client.complete_response(self.first())
        self.adapter.save_profile('native', json.dumps(context()).encode())
        self.assertTrue(self.upstream.device.closed.wait(0.5))
        self.assertEqual(self.client.responses, {})
        self.adapter.reload('native')
        with patch.object(self.client, 'close', wraps=self.client.close) as close:
            self.adapter.delete_profile('native')
            close.assert_called_once()

    def test_changed_snapshot_closes_old_client_and_invalidates_responses(self):
        first = self.client.complete_response(self.first())
        replacement = NativeClient(context(), send=self.upstream.send,
                                   catalog_reader=lambda *args, **kwargs: (MODELS, DEFAULTS))
        self.session_file.write_text(json.dumps(context()) + '\n')
        with patch('server.NativeClient', return_value=replacement):
            self.adapter.reload()
        self.assertTrue(self.upstream.device.closed.wait(0.5))
        self.assertEqual(self.client.responses, {})
        with self.assertRaises(ValueError):
            replacement.validate_request(self.continuation(first), '')

    def test_invalid_replacement_still_closes_old_device_stream(self):
        self.client.complete_response(self.first())
        self.session_file.write_text('{bad snapshot')
        with self.assertRaises(ValueError):
            self.adapter.reload()
        self.assertTrue(self.upstream.device.closed.wait(0.5))
        self.assertEqual(self.client.responses, {})

    def test_two_profiles_cannot_compete_for_the_same_device(self):
        first = self.client.complete_response(self.first())
        other = NativeClient(context(), send=self.upstream.send,
                             catalog_reader=lambda *args, **kwargs: (MODELS, DEFAULTS))
        self.addCleanup(other.close)
        self.adapter.save_profile('other', json.dumps(context()).encode())
        with patch('server.NativeClient', return_value=other):
            self.adapter.reload('other')
        with self.assertRaisesRegex(UpstreamError, 'native_device_busy'):
            self.adapter.validate_native_request(self.first(), '')
        self.assertEqual(self.upstream.count(DEVICE_STREAM), 1)
        self.adapter.reload()
        self.client.complete_response(self.continuation(first))

    def test_cancelled_header_wait_keeps_device_slot_until_reader_exits(self):
        release = threading.Event()
        self.addCleanup(release.set)

        def delayed_headers(path, payload, **kwargs):
            release.wait(1)
            return self.upstream.device

        self.client.send_request = delayed_headers
        self.client.connect_timeout = 0.02
        with self.assertRaisesRegex(UpstreamError, 'native_device_not_ready'):
            self.client.complete_response(self.first())
        replacement = NativeClient(context(), send=self.upstream.send,
                                   catalog_reader=lambda *args, **kwargs: (MODELS, DEFAULTS))
        self.session_file.write_text(json.dumps(context()) + '\n')
        with patch('server.NativeClient', return_value=replacement):
            self.adapter.reload()
        with self.assertRaisesRegex(UpstreamError, 'native_device_busy'):
            self.adapter.validate_native_request(self.first(), '')
        release.set()
        self.client.active.threads[0].join(0.5)
        self.adapter.validate_native_request(self.first(), '')
        self.assertEqual(self.upstream.count('/chat/completion'), 0)


if __name__ == '__main__':
    unittest.main()
