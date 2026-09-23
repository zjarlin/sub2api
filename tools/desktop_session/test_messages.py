import copy
import json
import unittest
from api import parse_request
from messages import parse_messages


def call_message():
    return {'role': 'assistant', 'content': None, 'tool_calls': [
        {'id': 'call_test', 'type': 'function', 'function': {'name': 'lookup', 'arguments': '{"query":"alpha"}'}}]}


class MessageTests(unittest.TestCase):
    def setUp(self):
        self.user = {'role': 'user', 'content': 'Find alpha'}
        self.result = {'role': 'tool', 'tool_call_id': 'call_test', 'content': 'RESULT_MARKER'}
        self.messages = [self.user, call_message(), self.result]

    def test_tool_result_can_finish_request_without_synthetic_user_message(self):
        request = parse_request({'model': 'doubao-pro', 'messages': self.messages})
        self.assertEqual(json.loads(request.text.split('\n', 1)[1]), self.messages)
        self.assertFalse(request.tools.enabled)

    def test_parallel_results_may_arrive_in_either_order(self):
        assistant = call_message()
        second = copy.deepcopy(assistant['tool_calls'][0])
        second['id'] = 'call_second'
        assistant['tool_calls'].append(second)
        messages = [self.user, assistant, {**self.result, 'tool_call_id': 'call_second'}, self.result]
        self.assertEqual(parse_messages(messages), messages)

    def test_missing_or_duplicate_results_and_unknown_ids_fail(self):
        invalid = [self.messages[:-1], [self.user, self.result], self.messages + [self.result],
                   [self.user, call_message(), self.user],
                   [self.user, call_message(), {**self.result, 'tool_call_id': 'unknown'}]]
        for messages in invalid:
            with self.subTest(messages=messages), self.assertRaises(ValueError):
                parse_messages(messages)

    def test_duplicate_call_ids_fail_even_after_prior_result(self):
        with self.assertRaises(ValueError):
            parse_messages(self.messages + [call_message(), self.result])

    def test_invalid_historical_arguments_fail(self):
        for arguments in ('bad json', '[]', '{"query":NaN}', '```json\n{}\n```'):
            messages = copy.deepcopy(self.messages)
            messages[1]['tool_calls'][0]['function']['arguments'] = arguments
            with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                parse_messages(messages)

    def test_text_parts_are_normalized_but_images_are_rejected(self):
        messages = [{'role': 'user', 'content': [{'type': 'text', 'text': 'hello'}, {'type': 'text', 'text': ' world'}]}]
        self.assertEqual(parse_messages(messages), [{'role': 'user', 'content': 'hello world'}])
        with self.assertRaises(ValueError):
            parse_messages([{'role': 'user', 'content': [{'type': 'image_url', 'image_url': {'url': 'data:test'}}]}])

    def test_empty_assistant_tool_list_is_text_only(self):
        messages = [{'role': 'assistant', 'content': 'hello', 'tool_calls': []}, self.user]
        self.assertNotIn('tool_calls', parse_messages(messages)[0])

    def test_continuation_preserves_history_for_every_text_role(self):
        for role in ('assistant', 'system', 'developer', 'user'):
            for content in ('Continue the pending task', '', []):
                with self.subTest(role=role, content=content):
                    messages = [self.user, {'role': role, 'content': content}]
                    request = parse_request({'model': 'doubao-pro', 'messages': messages})
                    expected = [self.user, {'role': role, 'content': content if isinstance(content, str) else ''}]
                    self.assertEqual(json.loads(request.text.split('\n', 1)[1]), expected)

    def test_single_message_preserves_non_user_roles(self):
        for role in ('assistant', 'system', 'developer'):
            with self.subTest(role=role):
                messages = [{'role': role, 'content': 'Continue the pending task'}]
                request = parse_request({'model': 'doubao-pro', 'messages': messages})
                self.assertEqual(json.loads(request.text.split('\n', 1)[1]), messages)

    def test_single_user_message_keeps_text_fast_path(self):
        request = parse_request({'model': 'doubao-pro', 'messages': [self.user]})
        self.assertEqual(request.text, self.user['content'])

    def test_empty_single_user_message_keeps_role_context(self):
        messages = [{'role': 'user', 'content': ''}]
        request = parse_request({'model': 'doubao-pro', 'messages': messages})
        self.assertEqual(json.loads(request.text.split('\n', 1)[1]), messages)

    def test_assistant_commentary_after_tool_results_can_continue(self):
        messages = self.messages + [{'role': 'assistant', 'content': 'I will summarize the tool results.'}]
        request = parse_request({'model': 'doubao-pro', 'messages': messages})
        self.assertEqual(json.loads(request.text.split('\n', 1)[1]), messages)
