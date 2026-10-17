"""合成设备帧的离线测试；不代表豆包云端闭环验收。"""
import copy
import json
import unittest
from native_tools import (NativeProtocolError, NativeSandboxCommands, NativeToolExchange,
                          connector_task_input, connector_task_input_from_catalog)


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


class NativeCatalogTests(unittest.TestCase):
    def setUp(self):
        self.runtime = {'connectorId': 'connector-a', 'status': 'READY', 'enabled': True}
        self.tool = {'name': 'lookup', 'description': 'Return a marker',
                     'inputSchema': {'type': 'object', 'properties': {'key': {'type': 'string'}}},
                     'annotations': {'readOnlyHint': True}, '_meta': {'version': '1'}}
        self.entry = {'connectorId': 'connector-a', 'toolsJson': json.dumps([self.tool])}
        self.catalog = {'isOK': True, 'data': {'connectors': [self.entry]}}

    def test_ready_catalog_defines_tools_without_prompt_or_invented_identity(self):
        task = json.loads(connector_task_input_from_catalog(self.runtime, self.catalog))
        connector = task['localConnectors'][0]
        self.assertEqual(connector['connectorId'], self.runtime['connectorId'])
        tool = connector['mcpToolsList'][0]
        self.assertEqual(tool['name'], self.tool['name'])
        self.assertEqual(json.loads(tool['inputSchema']), self.tool['inputSchema'])
        self.assertEqual(json.loads(tool['annotations']), self.tool['annotations'])
        self.assertEqual(json.loads(tool['meta']), self.tool['_meta'])

    def test_synthetic_connector_absent_from_runtime_catalog_is_rejected(self):
        synthetic = {**self.runtime, 'connectorId': 'synthetic-marker'}
        with self.assertRaisesRegex(NativeProtocolError, 'matching connector'):
            connector_task_input_from_catalog(synthetic, self.catalog)

    def test_installing_disabled_or_missing_runtime_cannot_supply_tools(self):
        states = ['NOT_INSTALLED', 'INSTALLING', 'INSTALL_FAILED', 'STARTING', 'START_FAILED',
                  'AUTH_REQUIRED', 'AUTHORIZING', 'AUTH_FAILED', None]
        variants = [{**self.runtime, 'status': status} for status in states]
        variants += [None, {**self.runtime, 'enabled': False}, {**self.runtime, 'enabled': 1}]
        for runtime in variants:
            with self.subTest(runtime=runtime), self.assertRaises(NativeProtocolError):
                connector_task_input_from_catalog(runtime, self.catalog)

    def test_failed_ambiguous_or_corrupt_catalog_does_not_fall_back_to_injected_tools(self):
        variants = [None, {'isOK': False}, {'isOK': True, 'data': None},
                    {'isOK': True, 'data': {'connectors': {}}},
                    {'isOK': True, 'data': {'connectors': [None]}},
                    {'isOK': True, 'data': {'connectors': [self.entry, self.entry]}}]
        for catalog in variants:
            with self.subTest(catalog=catalog), self.assertRaises(NativeProtocolError):
                connector_task_input_from_catalog(self.runtime, catalog)
        for raw in ['bad-json', '{}', '[]', '[null]', json.dumps([self.tool, self.tool]),
                    json.dumps([{**self.tool, 'inputSchema': '{}'}]),
                    json.dumps([{**self.tool, 'annotations': []}]),
                    json.dumps([{**self.tool, '_meta': {'invalid': float('nan')}}])]:
            self.entry['toolsJson'] = raw
            with self.subTest(raw=raw), self.assertRaises(NativeProtocolError):
                connector_task_input_from_catalog(self.runtime, self.catalog)


class NativeSandboxTests(unittest.TestCase):
    def setUp(self):
        self.commands = NativeSandboxCommands('123', 'sandbox-a')
        self.detail = {'id': 'connection-a', 'sandbox_id': 'sandbox-a', 'agent_id': 'agent-a'}
        self.frame = {'event_type': 2, 'sandbox_id': 'sandbox-a', 'seq': 1,
                      'command': {'id': 'command-a', 'name': 'sandbox', 'need_ack': True,
                                  'input': json.dumps({'type': 1, 'connect': self.detail}),
                                  'option': {'agent_id': 'agent-a', 'task_id': 'task-a'}}}

    def test_connect_decode_requires_explicit_outcome_before_ack(self):
        command = self.commands.receive(self.frame)
        self.assertEqual(command, {'type': 'sandbox_command', 'command_id': 'command-a',
                         'action': 'connect', 'sandbox_id': 'sandbox-a', 'need_ack': True,
                         'connection_id': 'connection-a', 'agent_id': 'agent-a', 'task_id': 'task-a'})
        self.assertEqual(self.commands.results, {})
        failed = self.commands.result_payload('command-a', False, 'sandbox_not_prepared')
        self.assertEqual(failed, {'device_id': '123', 'command_result': {
                         'id': 'command-a', 'code': 1, 'message': 'sandbox_not_prepared'}})
        self.assertEqual(failed, self.commands.result_payload('command-a', False, 'sandbox_not_prepared'))
        with self.assertRaises(NativeProtocolError):
            self.commands.result_payload('command-a', True)

    def test_no_ack_command_never_produces_upload_payload(self):
        self.frame['command']['need_ack'] = False
        self.commands.receive(self.frame)
        self.assertIsNone(self.commands.result_payload('command-a', False, 'sandbox_not_prepared'))
        self.assertEqual(self.commands.results['command-a']['code'], 1)

    def test_lifecycle_and_tool_results_remain_separate(self):
        tool_exchange = NativeToolExchange('123', 'sandbox-a', ['lookup'], 'connector-a')
        with self.assertRaises(NativeProtocolError):
            tool_exchange.receive(self.frame)
        self.commands.receive(self.frame)
        self.assertEqual(self.commands.result_payload('command-a', True)['command_result']['code'], 0)
        tool_frame = {'event_type': 1, 'sandbox_id': 'sandbox-a', 'tool_call': {
            'id': 'call-a', 'name': 'connector.call', 'input': json.dumps({
                'protocol_version': 1, 'connector_id': 'connector-a', 'remote_tool_name': 'lookup',
                'raw_arguments': '{"key":"offline"}'})}}
        call = tool_exchange.receive(tool_frame)
        result = tool_exchange.connector_result_payload(call['call_id'], '{"content":[]}')
        self.assertIn('tool_result', result)
        self.assertNotIn('command_result', result)
        for kind, action in [(2, 'disconnect'), (3, 'release')]:
            command = {**self.frame['command'], 'id': action,
                       'input': json.dumps({'type': kind, action: self.detail})}
            decoded = self.commands.receive({**self.frame, 'command': command})
            self.assertEqual(decoded['action'], action)
            self.assertEqual(self.commands.result_payload(action, True)['command_result']['id'], action)

    def test_duplicate_transport_frame_does_not_repeat_lifecycle_operation(self):
        self.commands.receive(self.frame)
        self.assertIsNone(self.commands.receive({**self.frame, 'seq': 2}))
        self.frame['command']['need_ack'] = False
        with self.assertRaises(NativeProtocolError):
            self.commands.receive(self.frame)

    def test_unsupported_or_misrouted_commands_are_never_accepted(self):
        variants = [None, {'event_type': 1}, {**self.frame, 'event_type': True},
                    {**self.frame, 'sandbox_id': 'other'}, {**self.frame, 'sandbox_id': None},
                    {**self.frame, 'signature': {'device_id': 'other'}}]
        for key, value in [('name', 'workspace.apply'), ('input', 'bad-json'), ('input', '[]'),
                           ('need_ack', 'true'), ('option', []), ('id', ''),
                           ('option', {'agent_id': 'other'}), ('input', '{"type":true}')]:
            variants.append({**self.frame, 'command': {**self.frame['command'], key: value}})
        for detail in [None, {**self.detail, 'sandbox_id': 'other'}, {**self.detail, 'id': []}]:
            command = {**self.frame['command'], 'input': json.dumps({'type': 1, 'connect': detail})}
            variants.append({**self.frame, 'command': command})
        for frame in variants:
            with self.subTest(frame=frame), self.assertRaises(NativeProtocolError):
                self.commands.receive(frame)
        self.assertEqual(self.commands.commands, {})
        with self.assertRaises(NativeProtocolError):
            self.commands.result_payload('unknown', True)
