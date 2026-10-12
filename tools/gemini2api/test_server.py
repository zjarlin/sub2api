import json
import threading
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler
from server import RequestError, Server, as_response, response_events, to_chat


class Upstream(BaseHTTPRequestHandler):
    requests = []
    failure = False

    def log_message(self, *_args):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        self.requests.append((self.path, self.headers['Authorization'], body))
        if self.failure:
            self.send_response(429)
            self.end_headers()
            self.wfile.write(b'secret upstream key')
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(json.dumps({'id': 'chat_test', 'object': 'chat.completion', 'model': body['model'],
            'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': '你好'}, 'finish_reason': 'stop'}],
            'usage': {'prompt_tokens': 4, 'completion_tokens': 2, 'total_tokens': 6}}).encode())


class AdapterTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.upstream = Server(('127.0.0.1', 0), 'unused', 'unused', [])
        cls.upstream.RequestHandlerClass = Upstream
        cls.adapter = Server(('127.0.0.1', 0), 'adapter-secret', 'google-secret', ['gemini-test'],
                             'http://127.0.0.1:' + str(cls.upstream.server_port))
        for server in (cls.upstream, cls.adapter):
            threading.Thread(target=server.serve_forever, daemon=True).start()
        cls.base = 'http://127.0.0.1:' + str(cls.adapter.server_port)

    @classmethod
    def tearDownClass(cls):
        for server in (cls.adapter, cls.upstream):
            server.shutdown()
            server.server_close()

    def setUp(self):
        Upstream.requests.clear()
        Upstream.failure = False

    def call(self, path, body=None, key='adapter-secret'):
        request = urllib.request.Request(self.base + path,
            data=json.dumps(body).encode() if body is not None else None,
            headers={'Authorization': 'Bearer ' + key, 'Content-Type': 'application/json'})
        try:
            with urllib.request.urlopen(request) as result:
                return result.status, result.read().decode()
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read().decode()

    def test_auth_and_models(self):
        self.assertEqual(self.call('/v1/models', key='bad')[0], 401)
        self.assertEqual(json.loads(self.call('/v1/models')[1])['data'][0]['id'], 'gemini-test')
        self.assertEqual(self.call('/livez', key='bad')[0], 200)

    def test_responses_history_and_usage(self):
        status, raw = self.call('/v1/responses', {'model': 'gemini-test', 'instructions': '简短',
            'input': [{'role': 'user', 'content': [{'type': 'input_text', 'text': '你好'}]},
                      {'role': 'assistant', 'content': [{'type': 'output_text', 'text': '好'}]},
                      {'role': 'user', 'content': '继续'}], 'max_output_tokens': 20})
        self.assertEqual(status, 200)
        result = json.loads(raw)
        self.assertEqual(result['usage']['total_tokens'], 6)
        self.assertEqual(result['status'], 'completed')
        path, key, body = Upstream.requests[0]
        self.assertEqual(path, '/chat/completions')
        self.assertEqual(key, 'Bearer google-secret')
        self.assertEqual(body['messages'][0]['role'], 'system')
        self.assertEqual(body['max_tokens'], 20)
        self.assertFalse(body['stream'])

    def test_response_stream_identity_and_terminal(self):
        status, raw = self.call('/v1/responses', {'model': 'gemini-test', 'input': 'hi', 'stream': True})
        self.assertEqual(status, 200)
        events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith('data: ')]
        self.assertEqual([event['sequence_number'] for event in events], list(range(9)))
        self.assertEqual(events[-1]['type'], 'response.completed')
        self.assertEqual(events[2]['item']['id'], events[-1]['response']['output'][0]['id'])
        self.assertEqual(events[0]['response']['output'], [])

    def test_chat_stream_usage(self):
        status, raw = self.call('/v1/chat/completions', {'model': 'gemini-test',
            'messages': [{'role': 'user', 'content': 'hi'}], 'stream': True, 'stream_options': {'include_usage': True}})
        self.assertEqual(status, 200)
        self.assertTrue(raw.endswith('data: [DONE]\n\n'))
        self.assertIn('"total_tokens": 6', raw)

    def test_rejections_do_not_call_upstream(self):
        for extra in ({'tools': [{'type': 'function'}]}, {'previous_response_id': 'resp_old'},
                      {'input': [{'role': 'user', 'content': [{'type': 'input_image', 'image_url': 'x'}]}]},
                      {'model': 'unknown'}, {'input': [{'type': 'function_call_output'}]}, {'max_output_tokens': -1},
                      {'text': {'format': {'type': 'json_schema'}}}, {'audio': {'voice': 'alloy'}},
                      {'modalities': ['text', 'audio']}):
            status, _ = self.call('/v1/responses', dict({'model': 'gemini-test', 'input': 'hi'}, **extra))
            self.assertEqual(status, 400)
        self.assertEqual(Upstream.requests, [])

    def test_codex_plain_text_options(self):
        status, raw = self.call('/v1/responses', {'model': 'gemini-test', 'input': 'hi',
            'text': {'format': {'type': 'text'}, 'verbosity': 'low'}, 'store': False})
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(raw)['status'], 'completed')

    def test_proxy_is_explicit(self):
        server = Server(('127.0.0.1', 0), 'unused', 'unused', [], upstream_proxy='http://localhost:9999')
        try:
            proxies = [handler.proxies for handler in server.opener.handlers if isinstance(handler, urllib.request.ProxyHandler)]
            self.assertEqual(proxies, [{'http': 'http://localhost:9999', 'https': 'http://localhost:9999'}])
        finally:
            server.server_close()

    def test_upstream_error_is_not_stream_success(self):
        Upstream.failure = True
        status, raw = self.call('/v1/responses', {'model': 'gemini-test', 'input': 'hi', 'stream': True})
        self.assertEqual(status, 429)
        self.assertNotIn('secret upstream', raw)
        self.assertNotIn('response.completed', raw)

    def test_incomplete_and_malformed_answers(self):
        chat = {'choices': [{'message': {'content': 'partial'}, 'finish_reason': 'length'}]}
        response = as_response(chat, 'gemini-test')
        self.assertEqual(list(response_events(response))[-1][0], 'response.incomplete')
        chat['choices'][0]['finish_reason'] = 'tool_calls'
        with self.assertRaises(ValueError):
            as_response(chat, 'gemini-test')


if __name__ == '__main__':
    unittest.main()
