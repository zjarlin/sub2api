import json
import unittest
from types import SimpleNamespace
from api import completion, encode_stream, parse_request


class ApiTests(unittest.TestCase):
    def test_invalid_tool_definitions_and_generation_controls_are_rejected(self):
        for extra in [{'tools': [{'type': 'function'}]}, {'max_tokens': 1}, {'n': 2}]:
            with self.assertRaises(ValueError):
                parse_request({'model': '9', 'messages': [{'role': 'user', 'content': 'hi'}], **extra})

    def test_multi_turn_context_is_explicit_not_shared_server_state(self):
        messages = [{'role': 'user', 'content': 'The number is 7'},
                    {'role': 'assistant', 'content': 'Understood'},
                    {'role': 'user', 'content': 'What number?'}]
        request = parse_request({'model': '9', 'messages': messages})
        self.assertEqual(json.loads(request.text.split('\n', 1)[1]), messages)
        self.assertFalse(request.stream)

    def test_buffered_stream_has_same_answer_and_no_invented_usage(self):
        result = completion('9', SimpleNamespace(text='hello'))
        raw = encode_stream(result).decode()
        events = [json.loads(line[6:]) for line in raw.splitlines()
                  if line.startswith('data: {')]
        self.assertEqual(events[0]['choices'][0]['delta']['content'], 'hello')
        self.assertEqual(events[-1]['choices'][0]['finish_reason'], 'stop')
        self.assertTrue(raw.endswith('data: [DONE]\n\n'))
        self.assertNotIn('usage', result)
