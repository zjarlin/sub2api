import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import test_server
from reply import Reply
from responses import completion, encode_stream, parse_request, validate_history
from responses_sessions import ResponsesStore


def function(namespace=False):
    tool = {'type': 'function', 'name': 'lookup', 'parameters': {
        'type': 'object', 'properties': {'key': {'type': 'string'}}, 'required': ['key']}}
    return {'type': 'namespace', 'name': 'local', 'tools': [tool]} if namespace else tool


def reply(text='answer', index=2):
    return Reply(conversation_id='conv', section_id='section', message_id='message',
                 message_index=index, blocks={'text': text}, finished=True, ended=True)


class ResponsesTests(unittest.TestCase):
    def test_input_preserves_namespace_phase_reasoning_and_long_tool_history(self):
        items = [{'role': 'assistant', 'phase': 'commentary', 'content': 'Working'}]
        for index in range(150):
            items.extend([
                {'type': 'function_call', 'call_id': str(index), 'name': 'lookup', 'namespace': 'local', 'arguments': '{"key":"x"}'},
                {'type': 'function_call_output', 'call_id': str(index), 'output': 'result'},
            ])
        items.append({'type': 'reasoning', 'summary': [{'type': 'summary_text', 'text': 'Use results'}]})
        req = parse_request({'model': 'doubao-pro', 'input': items, 'tools': [function(True)]})
        validate_history(req.items)
        self.assertEqual(len(req.items), 302)
        self.assertEqual(req.items[0]['phase'], 'commentary')
        self.assertEqual(req.items[1]['namespace'], 'local')
        self.assertEqual(req.tools['tools'][0]['namespace'], 'local')

    def test_call_output_pairing_rejects_unknown_duplicate_missing_and_wrong_type(self):
        call = {'type': 'function_call', 'name': 'lookup', 'call_id': 'a', 'arguments': '{}'}
        output = {'type': 'function_call_output', 'call_id': 'a', 'output': 'ok'}
        for items in ([output], [call], [call, output, output], [call, call, output],
                      [call, {**output, 'type': 'custom_tool_call_output'}]):
            with self.subTest(items=items), self.assertRaises(ValueError):
                validate_history(parse_request({'model': 'doubao-pro', 'input': items}).items)

    def test_function_and_custom_output_and_sse_lifecycle(self):
        tools = [function(True), {'type': 'custom', 'name': 'patch', 'format': {'type': 'text'}}]
        req = parse_request({'model': 'doubao-pro', 'input': 'work', 'tools': tools, 'tool_choice': 'required'})
        result = completion(req, 'doubao-pro', reply(json.dumps({'text': None, 'calls': [
            {'type': 'function', 'namespace': 'local', 'name': 'lookup', 'arguments': {'key': 'x'}},
            {'type': 'custom', 'name': 'patch', 'input': 'a\nb'},
        ]})))
        self.assertEqual([x['type'] for x in result['output']], ['function_call', 'custom_tool_call'])
        self.assertEqual(result['tools'], tools)
        self.assertEqual(result['output'][0]['namespace'], 'local')
        self.assertEqual(result['output'][1]['input'], 'a\nb')
        self.assertEqual(len({x['call_id'] for x in result['output']}), 2)
        events = [json.loads(line[6:]) for line in encode_stream(result).decode().splitlines() if line.startswith('data: ')]
        self.assertEqual([e['sequence_number'] for e in events], list(range(len(events))))
        self.assertEqual(events[0]['type'], 'response.created')
        self.assertEqual(events[-1]['type'], 'response.completed')
        self.assertEqual(events[-1]['response'], result)
        self.assertIn('response.function_call_arguments.done', [e['type'] for e in events])
        self.assertIn('response.custom_tool_call_input.done', [e['type'] for e in events])

    def test_unsupported_media_tools_and_constraints_fail_before_generation(self):
        for extra in ({'tools': [{'type': 'web_search'}]}, {'background': True},
                      {'tools': [{'type': 'custom', 'name': 'patch', 'format': {'type': 'grammar', 'syntax': 'lark', 'definition': '%import secret.DATA\nstart: DATA'}}]},
                      {'input': [{'role': 'user', 'content': [{'type': 'input_image', 'image_url': 'x'}]}]}):
            with self.subTest(extra=extra), self.assertRaises(ValueError):
                parse_request({'model': 'doubao-pro', 'input': 'hello', **extra})

    def test_custom_grammar_is_validated_and_foreign_reasoning_is_not_prompted(self):
        from responses import input_prompt
        for syntax, definition in [('lark', 'start: "PATCH"'), ('regex', 'PATCH')]:
            req = parse_request({'model': 'doubao-pro', 'input': [
                {'type': 'reasoning', 'summary': [], 'encrypted_content': 'PRIVATE_CIPHERTEXT'},
                {'role': 'user', 'content': 'patch'}], 'tools': [
                    {'type': 'custom', 'name': 'patch', 'format': {'type': 'grammar', 'syntax': syntax, 'definition': definition}}]})
            self.assertNotIn('PRIVATE_CIPHERTEXT', input_prompt(req, req.items))
            for value in ('PATCH', 'INVALID'):
                decision = reply(json.dumps({'text': None, 'calls': [{'type': 'custom', 'name': 'patch', 'input': value}]}))
                if value == 'PATCH':
                    self.assertEqual(completion(req, req.model, decision)['output'][0]['input'], value)
                else:
                    with self.assertRaises(ValueError):
                        completion(req, req.model, decision)

    def test_text_format_and_tool_policy_are_enforced(self):
        req = parse_request({'model': 'doubao-pro', 'input': 'work', 'tools': [function()],
                             'parallel_tool_calls': False, 'tool_choice': 'required'})
        for calls in ([], [{'type': 'function', 'name': 'lookup', 'arguments': {}}],
                      [{'type': 'function', 'name': 'lookup', 'arguments': {'key': 'x'}}] * 2):
            with self.subTest(calls=calls), self.assertRaises(ValueError):
                completion(req, 'doubao-pro', reply(json.dumps({'text': None, 'calls': calls})))
        req = parse_request({'model': 'doubao-pro', 'input': 'work', 'text': {'format': {'type': 'json_object'}}})
        with self.assertRaises(ValueError):
            completion(req, 'doubao-pro', reply('not json'))

    def test_client_tool_search_preserves_wire_types_and_loads_discovered_namespace(self):
        req = parse_request({'model': 'doubao-pro', 'input': 'find a tool', 'tools': [{'type': 'tool_search', 'execution': 'client'}]})
        result = completion(req, req.model, reply('{"text":null,"calls":[{"type":"tool_search","name":"tool_search","arguments":{"query":"local"}}]}'))
        call = result['output'][0]
        self.assertEqual(call['type'], 'tool_search_call')
        self.assertEqual(call['arguments'], {'query': 'local'})
        follow = parse_request({'model': req.model, 'input': [call, {
            'type': 'tool_search_output', 'call_id': call['call_id'], 'tools': [function(True)]}],
            'tools': [{'type': 'tool_search', 'execution': 'client'}]})
        validate_history(follow.items)
        self.assertEqual(follow.tools['tools'][1]['namespace'], 'local')
        self.assertIn(b'tool_search_call', encode_stream(result))
        self.assertNotIn(b'custom_tool_call_input', encode_stream(result))

    def test_identical_first_turns_do_not_share_a_cursor_or_cached_response(self):
        store = ResponsesStore()
        req = parse_request({'model': 'doubao-pro', 'input': 'hello'})
        result = completion(req, req.model, reply())
        store.commit(req, req.model, 'tenant', reply(), result, store.plan(req, req.model, 'tenant'))
        plan = store.plan(req, req.model, 'tenant')
        self.assertIsNone(plan.cached)
        self.assertIsNone(plan.cursor)

    def test_previous_response_incremental_retry_branch_and_scope(self):
        store = ResponsesStore()
        first = parse_request({'model': 'doubao-pro', 'input': 'remember alpha', 'instructions': 'Be concise'})
        plan = store.plan(first, first.model, 'task')
        result = completion(first, first.model, reply())
        store.commit(first, first.model, 'task', reply(), result, plan)
        second = parse_request({'model': first.model, 'input': 'next', 'previous_response_id': result['id'], 'instructions': 'Be concise'})
        for scope, model in [('other', first.model), ('task', 'doubao-auto')]:
            with self.assertRaises(ValueError):
                store.plan(second, model, scope)
        plan = store.plan(second, first.model, 'task')
        self.assertEqual(plan.cursor, reply().cursor)
        self.assertNotIn('remember alpha', plan.text)
        self.assertIn('next', plan.text)
        second_result = completion(second, first.model, reply('next answer', 4))
        store.commit(second, first.model, 'task', reply('next answer', 4), second_result, plan)
        self.assertEqual(store.plan(second, first.model, 'task').cached['id'], second_result['id'])
        branch = parse_request({'model': first.model, 'input': 'branch', 'previous_response_id': result['id']})
        plan = store.plan(branch, first.model, 'task')
        self.assertIsNone(plan.cursor)
        self.assertIn('remember alpha', plan.text)

    def test_store_false_privacy_and_bounded_state(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'responses.json'
            store = ResponsesStore(path)
            req = parse_request({'model': 'doubao-pro', 'input': 'PRIVATE_INPUT', 'store': False})
            plan = store.plan(req, req.model)
            result = completion(req, req.model, reply('PRIVATE_OUTPUT'))
            store.commit(req, req.model, '', reply(), result, plan)
            self.assertNotIn('PRIVATE_', path.read_text())
            next_req = parse_request({'model': req.model, 'input': 'next', 'previous_response_id': result['id']})
            with self.assertRaises(ValueError):
                store.plan(next_req, req.model)
            store.max_bytes = 100
            store.persist()
            self.assertFalse(store.entries)

    def test_stored_history_survives_restart_and_expires(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'responses.json'
            store = ResponsesStore(path, clock=lambda: 1)
            req = parse_request({'model': 'doubao-pro', 'input': 'hello'})
            result = completion(req, req.model, reply())
            store.commit(req, req.model, '', reply(), result, store.plan(req, req.model))
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            resumed = parse_request({'model': req.model, 'input': 'next', 'previous_response_id': result['id']})
            self.assertIsNotNone(ResponsesStore(path, clock=lambda: 2).plan(resumed, req.model).cursor)
            with self.assertRaises(ValueError):
                ResponsesStore(path, clock=lambda: 30000).plan(resumed, req.model)


class ResponsesServerTests(unittest.TestCase):
    setUp = test_server.ServerTests.setUp

    def request(self, body):
        return test_server.ServerTests.request(self, json.dumps(body).encode(), path='/v1/responses')

    def test_responses_never_calls_chat_parser_or_completion(self):
        with patch('server.parse_request', side_effect=AssertionError('Chat parser called')), \
                patch('server.completion', side_effect=AssertionError('Chat completion called')):
            for stream in (False, True):
                status, raw = self.request({'model': 'doubao-pro', 'input': 'hello', 'stream': stream, 'store': False})
                self.assertEqual(status, 200)
                if stream:
                    self.assertIn(b'event: response.completed', raw)
                    self.assertNotIn(b'chat.completion', raw)
                else:
                    self.assertEqual(json.loads(raw)['object'], 'response')

    def test_previous_id_tool_result_roundtrip_and_retry(self):
        self.client.complete.return_value = reply('{"text":null,"calls":[{"type":"function","name":"lookup","arguments":{"key":"x"}}]}')
        status, raw = self.request({'model': 'doubao-pro', 'input': 'lookup x', 'tools': [function()], 'tool_choice': 'required'})
        self.assertEqual(status, 200)
        first = json.loads(raw)
        self.client.complete.return_value = reply('CLIENT_MARKER', 4)
        follow = {'model': 'doubao-pro', 'previous_response_id': first['id'],
                  'input': [{'type': 'function_call_output', 'call_id': first['output'][0]['call_id'], 'output': 'CLIENT_MARKER'}]}
        status, raw = self.request(follow)
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(raw)['output'][0]['content'][0]['text'], 'CLIENT_MARKER')
        self.assertIsNotNone(self.client.complete.call_args.kwargs['cursor'])
        self.client.complete.reset_mock()
        self.assertEqual(self.request(follow)[1], raw)
        self.client.complete.assert_not_called()

    def test_bad_history_does_not_call_upstream_and_returns_400(self):
        for body in ({'input': [{'type': 'function_call_output', 'call_id': 'missing', 'output': 'fake'}]},
                     {'input': 'next', 'previous_response_id': 'unknown'}):
            status, raw = self.request({'model': 'doubao-pro', **body})
            self.assertEqual(status, 400)
            self.assertEqual(json.loads(raw)['error']['code'], 'invalid_request')
            self.client.complete.assert_not_called()
