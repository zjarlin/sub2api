"""合成设备帧的离线测试；不代表豆包云端闭环验收。"""
import copy
import json
import unittest
from native_tools import NativeProtocolError, NativeToolExchange, connector_task_input


class NativeToolTests(unittest.TestCase):
    def setUp(self):
        self.exchange = NativeToolExchange('123', 'sandbox-a', ['lookup'])
        self.frame = {'event_type': 1, 'sandbox_id': 'sandbox-a',
                      'tool_call': {'id': 'call-a', 'name': 'lookup', 'input': '{"key":"你好"}'}}

    def test_call_and_result_preserve_upstream_identity(self):
        call = self.exchange.receive(self.frame)
        self.assertEqual(call['arguments'], self.frame['tool_call']['input'])
        self.assertEqual(call['call_id'], 'call-a')
        payload = self.exchange.result_payload(call['call_id'], 'unpredictable-result')
        self.assertEqual(payload['tool_result'], {'id': 'call-a', 'name': 'lookup',
                         'content': [{'type': 'text', 'text': 'unpredictable-result'}], 'isError': False})
        self.assertEqual(self.exchange.stream_payload()['client_attrs'], {'sandbox_id': 'sandbox-a'})

    def test_replay_does_not_dispatch_twice(self):
        self.exchange.receive(self.frame)
        self.assertIsNone(self.exchange.receive(copy.deepcopy(self.frame)))
        self.frame['tool_call']['input'] = '{}'
        with self.assertRaises(NativeProtocolError):
            self.exchange.receive(self.frame)

    def test_rejects_wrong_scope_commands_and_unregistered_tools(self):
        variants = [{'event_type': 2}, {'event_type': True}, {'event_type': 99},
                    {**self.frame, 'sandbox_id': 'other'}, {**self.frame, 'sandbox_id': None},
                    {**self.frame, 'signature': {'device_id': 'other'}}]
        for key, value in [('name', 'shell'), ('input', 'bad-json'), ('input', '[]'),
                           ('input', '{"value":NaN}')]:
            variants.append({**self.frame, 'tool_call': {**self.frame['tool_call'], key: value}})
        for frame in variants:
            with self.subTest(frame=frame), self.assertRaises(NativeProtocolError):
                self.exchange.receive(frame)
        self.assertEqual(self.exchange.calls, {})

    def test_heartbeat_is_not_an_execution_request(self):
        self.assertIsNone(self.exchange.receive({'event_type': 3}))

    def test_unknown_conflicting_and_error_results(self):
        with self.assertRaises(NativeProtocolError):
            self.exchange.result_payload('unknown', 'text')
        self.exchange.receive(self.frame)
        result = self.exchange.result_payload('call-a', 'failed', True)
        self.assertTrue(result['tool_result']['isError'])
        self.assertEqual(result, self.exchange.result_payload('call-a', 'failed', True))
        with self.assertRaises(NativeProtocolError):
            self.exchange.result_payload('call-a', 'success')


class NativeConnectorTests(unittest.TestCase):
    def setUp(self):
        self.exchange = NativeToolExchange('123', 'sandbox-a', ['lookup'], 'connector-a')
        self.envelope = {'protocol_version': 1, 'connector_id': 'connector-a',
                         'remote_tool_name': 'lookup', 'raw_arguments': '{"key":"hello"}'}
        self.frame = {'event_type': 1, 'sandbox_id': 'sandbox-a', 'seq': 1,
                      'tool_call': {'id': 'call-a', 'name': 'connector.call',
                                    'input': json.dumps(self.envelope)}}

    def test_definition_input_call_and_result_use_native_envelopes(self):
        function = {'name': 'lookup', 'description': 'Return a marker',
                    'parameters': {'type': 'object', 'properties': {'key': {'type': 'string'}}}}
        task = json.loads(connector_task_input('connector-a', [function]))
        tool = task['localConnectors'][0]['mcpToolsList'][0]
        self.assertEqual(json.loads(tool['inputSchema']), function['parameters'])
        call = self.exchange.receive(self.frame)
        self.assertEqual(call['name'], 'lookup')
        self.assertEqual(call['arguments'], self.envelope['raw_arguments'])
        raw = '{"content":[{"type":"text","text":"result"}],"isError":false}'
        result = self.exchange.connector_result_payload(call['call_id'], raw)
        self.assertEqual(result['tool_result']['raw_result'], raw)
        self.assertEqual(result['tool_result']['tool_input'], self.frame['tool_call']['input'])
        self.assertEqual(result['tool_result']['name'], 'connector.call')

    def test_replay_with_new_transport_sequence_does_not_execute_twice(self):
        self.exchange.receive(self.frame)
        self.assertIsNone(self.exchange.receive({**self.frame, 'seq': 2}))

    def test_wrong_connector_and_builtin_tools_never_become_function_calls(self):
        for key, value in [('connector_id', 'other'), ('protocol_version', True),
                           ('remote_tool_name', 'shell'), ('raw_arguments', {})]:
            call = {**self.frame['tool_call'], 'input': json.dumps({**self.envelope, key: value})}
            with self.subTest(key=key), self.assertRaises(NativeProtocolError):
                self.exchange.receive({**self.frame, 'tool_call': call})
        with self.assertRaises(NativeProtocolError):
            self.exchange.receive({**self.frame, 'tool_call': {**self.frame['tool_call'], 'name': 'Bash'}})
        self.assertEqual(self.exchange.calls, {})

    def test_connector_results_preserve_errors_and_reject_unknown_or_conflicting_results(self):
        with self.assertRaises(NativeProtocolError):
            self.exchange.connector_result_payload('unknown', '{}')
        self.exchange.receive(self.frame)
        raw = '{"error":{"code":-32602,"message":"invalid parameters"}}'
        result = self.exchange.connector_result_payload('call-a', raw)
        self.assertEqual(result['tool_result']['raw_result'], raw)
        self.assertEqual(result, self.exchange.connector_result_payload('call-a', raw))
        with self.assertRaises(NativeProtocolError):
            self.exchange.connector_result_payload('call-a', '{}')

    def test_invalid_tool_definitions_fail_before_request_construction(self):
        function = {'name': 'lookup', 'parameters': {'type': 'object'}}
        for definitions in [[], [None], [function, function],
                            [{**function, 'description': 42}], [{**function, 'parameters': []}]]:
            with self.subTest(definitions=definitions), self.assertRaises(NativeProtocolError):
                connector_task_input('connector-a', definitions)
