import copy
import json
import unittest
from types import SimpleNamespace
from api import completion, encode_stream, parse_request
from structured_output import OutputValidationError
from tool_calls import ToolCallValidationError


def function_tool(name='lookup'):
    return {'type': 'function', 'function': {
        'name': name, 'description': 'Look up the query', 'strict': True,
        'parameters': {'type': 'object', 'properties': {'query': {'type': 'string'}},
                       'required': ['query'], 'additionalProperties': False}}}


def tool_request(**options):
    return parse_request({'model': 'doubao-pro', 'tools': [function_tool()],
                          'messages': [{'role': 'user', 'content': 'Look up alpha'}], **options})


def finish(request, decision):
    return completion(request.model, SimpleNamespace(text=json.dumps(decision)),
                      request.output_format, request.tools)


class ToolCallTests(unittest.TestCase):
    def setUp(self):
        self.call = {'name': 'lookup', 'arguments': {'query': 'alpha'}}

    def test_required_emits_real_protocol_call_and_validates_arguments(self):
        request = tool_request(tool_choice='required', parallel_tool_calls=False)
        self.assertIn('客户端实际执行', request.text)
        result = finish(request, {'content': None, 'tool_calls': [self.call]})
        choice = result['choices'][0]
        self.assertEqual(choice['finish_reason'], 'tool_calls')
        call = choice['message']['tool_calls'][0]
        self.assertRegex(call['id'], r'^call_[a-f0-9]{32}$')
        self.assertEqual(call['type'], 'function')
        self.assertEqual(json.loads(call['function']['arguments']), {'query': 'alpha'})
        self.assertIsNone(choice['message']['content'])
        self.assertNotIn('usage', result)

    def test_auto_can_answer_without_calling(self):
        result = finish(tool_request(), {'content': 'answer', 'tool_calls': []})
        self.assertEqual(result['choices'][0]['message']['content'], 'answer')
        self.assertEqual(result['choices'][0]['finish_reason'], 'stop')

    def test_none_and_empty_catalog_remain_normal_text(self):
        for options in ({'tool_choice': 'none'}, {'tools': [], 'tool_choice': 'auto'}, {'tools': None}):
            request = tool_request(**options)
            self.assertFalse(request.tools.enabled)
            result = completion(request.model, SimpleNamespace(text='plain answer'), request.output_format, request.tools)
            self.assertEqual(result['choices'][0]['message'], {'role': 'assistant', 'content': 'plain answer'})

    def test_parallel_calls_have_unique_ids_and_sse_indices(self):
        request = tool_request(parallel_tool_calls=True)
        result = finish(request, {'content': None, 'tool_calls': [self.call, self.call]})
        calls = result['choices'][0]['message']['tool_calls']
        self.assertNotEqual(calls[0]['id'], calls[1]['id'])
        raw = encode_stream(result).decode()
        events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith('data: {')]
        deltas = events[0]['choices'][0]['delta']['tool_calls']
        self.assertEqual([call['index'] for call in deltas], [0, 1])
        self.assertEqual([call['id'] for call in deltas], [call['id'] for call in calls])
        self.assertEqual(events[-1]['choices'][0]['finish_reason'], 'tool_calls')
        self.assertTrue(raw.endswith('data: [DONE]\n\n'))

    def test_parallel_false_and_named_choice_enforce_one_call(self):
        for options in ({'parallel_tool_calls': False},
                        {'tool_choice': {'type': 'function', 'function': {'name': 'lookup'}}}):
            with self.subTest(options=options), self.assertRaises(ToolCallValidationError):
                finish(tool_request(**options), {'content': None, 'tool_calls': [self.call, self.call]})

    def test_named_choice_rejects_other_declared_function(self):
        request = tool_request(tools=[function_tool(), function_tool('other')],
                               tool_choice={'type': 'function', 'function': {'name': 'lookup'}})
        with self.assertRaises(ToolCallValidationError):
            finish(request, {'content': None, 'tool_calls': [{'name': 'other', 'arguments': {'query': 'alpha'}}]})

    def test_invalid_decisions_never_become_successful_tool_calls(self):
        invalid = [
            {'content': 'pretend success', 'tool_calls': []},
            {'content': None, 'tool_calls': [{'name': 'unknown', 'arguments': {}}]},
            {'content': None, 'tool_calls': [{'name': 'lookup', 'arguments': {}}]},
            {'content': None, 'tool_calls': [{'name': 'lookup', 'arguments': {'query': 1}}]},
            {'content': None, 'tool_calls': [{'name': 'lookup', 'arguments': {'query': 'a', 'extra': 1}}]},
            {'content': None, 'tool_calls': [{'name': 'lookup', 'arguments': '{"query":"a"}'}]},
            {'content': None, 'tool_calls': [dict(self.call, id='forged')]},
            {'content': None, 'tool_calls': 'lookup'},
            {'content': {}, 'tool_calls': [self.call]},
        ]
        request = tool_request(tool_choice='required')
        for decision in invalid:
            with self.subTest(decision=decision), self.assertRaises(ToolCallValidationError):
                finish(request, decision)

    def test_response_format_applies_to_final_answer_not_function_arguments(self):
        request = tool_request(response_format={'type': 'json_schema', 'json_schema': {
            'name': 'result', 'schema': {'type': 'object', 'properties': {'answer': {'type': 'integer'}},
                                       'required': ['answer'], 'additionalProperties': False}}})
        self.assertEqual(finish(request, {'content': None, 'tool_calls': [self.call]})['choices'][0]['finish_reason'],
                         'tool_calls')
        result = finish(request, {'content': '{"answer":42}', 'tool_calls': []})
        self.assertEqual(json.loads(result['choices'][0]['message']['content']), {'answer': 42})
        with self.assertRaises(OutputValidationError):
            finish(request, {'content': 'plain text', 'tool_calls': []})

    def test_malformed_policy_is_rejected_before_generation(self):
        invalid = [{'tools': {}}, {'tools': [function_tool()] * 129}, {'tools': [function_tool()] * 2},
                   {'parallel_tool_calls': 'false'}, {'tools': [], 'tool_choice': 'required'},
                   {'tool_choice': {'type': 'function', 'function': {'name': 'missing'}}},
                   {'tools': [{'type': 'custom', 'custom': {'name': 'shell'}}]}]
        for options in invalid:
            with self.subTest(options=options), self.assertRaises(ValueError):
                tool_request(**options)
        broken = copy.deepcopy(function_tool())
        broken['function']['parameters']['$ref'] = 'https://example.com/schema'
        with self.assertRaises(ValueError):
            tool_request(tools=[broken])
