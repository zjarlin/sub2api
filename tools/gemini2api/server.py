"""Gemini 纯文本问答适配器，仅使用 Python 标准库。"""
import copy
import hmac
import json
import os
import time
import uuid
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn

MAX_BODY = 2 * 1024 * 1024


class RequestError(Exception):
    pass


def text_content(content):
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        raise RequestError('content must be text')
    texts = []
    for part in content:
        if not isinstance(part, dict) or part.get('type') not in ('text', 'input_text', 'output_text') or not isinstance(part.get('text'), str):
            raise RequestError('Only text content is supported')
        texts.append(part['text'])
    return '\n'.join(texts)


def to_chat(body, responses, models):
    if not isinstance(body, dict):
        raise RequestError('Request must be an object')
    if body.get('tools') or body.get('functions') or body.get('function_call') or body.get('tool_choice') not in (None, 'none'):
        raise RequestError('This adapter only supports Q&A; tools are unsupported')
    if body.get('previous_response_id') or body.get('conversation') or body.get('background'):
        raise RequestError('Send complete text history; stored conversations are unsupported')
    if 'stream' in body and not isinstance(body['stream'], bool):
        raise RequestError('stream must be boolean')
    text_options = body.get('text', {})
    if not isinstance(text_options, dict):
        raise RequestError('text must be an object')
    text_format = text_options.get('format', {'type': 'text'})
    response_format = body.get('response_format', {'type': 'text'})
    if body.get('n', 1) != 1 or text_format != {'type': 'text'} or response_format != {'type': 'text'}:
        raise RequestError('Only a single plain text answer is supported')
    if body.get('audio') or body.get('modalities', ['text']) != ['text']:
        raise RequestError('Only text output is supported')
    if 'stream_options' in body and not isinstance(body['stream_options'], dict):
        raise RequestError('stream_options must be an object')
    model = body.get('model')
    if not isinstance(model, str) or model not in models:
        raise RequestError('Unknown model; use /v1/models')
    messages = []
    if responses:
        instructions = body.get('instructions')
        if instructions is not None:
            messages.append({'role': 'system', 'content': text_content(instructions)})
        inputs = body.get('input')
        if isinstance(inputs, str):
            inputs = [{'role': 'user', 'content': inputs}]
    else:
        inputs = body.get('messages')
    if not isinstance(inputs, list) or not inputs:
        raise RequestError('A nonempty text history is required')
    for message in inputs:
        if not isinstance(message, dict) or message.get('type', 'message') != 'message':
            raise RequestError('Only text messages are supported')
        role = message.get('role')
        if role not in ('system', 'developer', 'user', 'assistant') or message.get('tool_calls') or message.get('function_call'):
            raise RequestError('Only system/developer/user/assistant text messages are supported')
        messages.append({'role': role, 'content': text_content(message.get('content'))})
    result = {'model': model, 'messages': messages, 'stream': False}
    for key in ('temperature', 'top_p'):
        if key in body:
            value = body[key]
            if isinstance(value, bool) or not isinstance(value, (float, int)):
                raise RequestError(key + ' must be numeric')
            result[key] = value
    limit = body.get('max_output_tokens' if responses else 'max_tokens', body.get('max_completion_tokens'))
    if limit is not None:
        if type(limit) is not int or limit <= 0:
            raise RequestError('Token limit must be a positive integer')
        result['max_tokens'] = limit
    return result


def as_response(chat, model):
    choice = chat['choices'][0]
    if choice['finish_reason'] not in ('stop', 'length') or choice['message'].get('tool_calls'):
        raise ValueError('Upstream did not return a completed text answer')
    text = choice['message'].get('content')
    if not isinstance(text, str):
        raise ValueError('Upstream answer is not text')
    usage = chat.get('usage', {})
    status = 'incomplete' if choice['finish_reason'] == 'length' else 'completed'
    return {'id': 'resp_' + uuid.uuid4().hex, 'object': 'response', 'created_at': int(time.time()),
            'model': model, 'status': status, 'error': None,
            'incomplete_details': {'reason': 'max_output_tokens'} if status == 'incomplete' else None,
            'output': [{'id': 'msg_' + uuid.uuid4().hex, 'type': 'message', 'role': 'assistant',
                        'status': status, 'content': [{'type': 'output_text', 'text': text, 'annotations': []}]}],
            'usage': {'input_tokens': usage.get('prompt_tokens', 0),
                      'output_tokens': usage.get('completion_tokens', 0),
                      'total_tokens': usage.get('total_tokens', 0)}}


def response_events(response):
    item = response['output'][0]
    part = item['content'][0]
    pending = copy.deepcopy(response)
    pending.update(status='in_progress', output=[], usage=None, incomplete_details=None)
    yield 'response.created', {'response': pending}
    yield 'response.in_progress', {'response': pending}
    started = dict(item, status='in_progress', content=[])
    yield 'response.output_item.added', {'output_index': 0, 'item': started}
    yield 'response.content_part.added', {'item_id': item['id'], 'output_index': 0, 'content_index': 0, 'part': dict(part, text='')}
    coords = {'item_id': item['id'], 'output_index': 0, 'content_index': 0}
    yield 'response.output_text.delta', dict(coords, delta=part['text'])
    yield 'response.output_text.done', dict(coords, text=part['text'])
    yield 'response.content_part.done', dict(coords, part=part)
    yield 'response.output_item.done', {'output_index': 0, 'item': item}
    yield 'response.' + response['status'], {'response': response}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    # 禁止跨地址重定向，避免上游凭证被转发到其他服务。
    def redirect_request(self, request, fp, code, message, headers, new_url):
        return None


class Server(ThreadingMixIn, HTTPServer):
    daemon_threads = True

    def __init__(self, address, adapter_key, gemini_key, models, upstream='https://generativelanguage.googleapis.com/v1beta/openai', upstream_proxy=None):
        super().__init__(address, Handler)
        proxies = {'https': upstream_proxy, 'http': upstream_proxy} if upstream_proxy else {}
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler(proxies), NoRedirect())
        self.adapter_key = adapter_key
        self.gemini_key = gemini_key
        self.models = models
        self.upstream = upstream.rstrip('/')


class Handler(BaseHTTPRequestHandler):
    # 默认访问日志不记录请求、查询参数或凭证。
    def log_message(self, *_args):
        pass

    def send_json(self, status, body):
        data = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def authorized(self):
        return hmac.compare_digest(self.headers.get('Authorization', ''), 'Bearer ' + self.server.adapter_key)

    def error(self, status, message):
        self.send_json(status, {'error': {'message': message, 'type': 'invalid_request_error' if status == 400 else 'api_error'}})

    def do_GET(self):
        if self.path == '/livez':
            return self.send_json(200, {'status': 'ok'})
        if not self.authorized():
            return self.error(401, 'Invalid adapter key')
        if self.path == '/v1/models':
            return self.send_json(200, {'object': 'list', 'data': [{'id': model, 'object': 'model', 'owned_by': 'google'} for model in self.server.models]})
        self.error(404, 'Unknown endpoint')

    def do_POST(self):
        if not self.authorized():
            return self.error(401, 'Invalid adapter key')
        if self.path not in ('/v1/chat/completions', '/v1/responses'):
            return self.error(404, 'Unknown endpoint')
        try:
            if self.headers.get('Transfer-Encoding'):
                raise RequestError('Transfer-Encoding is unsupported')
            length = int(self.headers.get('Content-Length', '0'))
            if length <= 0 or length > MAX_BODY:
                raise RequestError('Request body must be between 1 byte and 2 MiB')
            body = json.loads(self.rfile.read(length))
            request = to_chat(body, self.path == '/v1/responses', self.server.models)
        except (RequestError, ValueError, UnicodeError) as exc:
            return self.error(400, str(exc))
        upstream = urllib.request.Request(self.server.upstream + '/chat/completions',
                                          data=json.dumps(request).encode(),
                                          headers={'Authorization': 'Bearer ' + self.server.gemini_key, 'Content-Type': 'application/json'})
        try:
            with self.server.opener.open(upstream, timeout=180) as result:
                raw = result.read(MAX_BODY + 1)
                if len(raw) > MAX_BODY:
                    raise ValueError('Upstream response exceeds limit')
                chat = json.loads(raw)
            response = as_response(chat, request['model'])
        except urllib.error.HTTPError as exc:
            # 上游错误正文可能含凭证，不直接透传。
            return self.error(exc.code if exc.code in (400, 401, 403, 429) else 502, 'Gemini upstream rejected the request')
        except (OSError, ValueError, KeyError, IndexError, TypeError):
            return self.error(502, 'Gemini upstream failed or returned an invalid answer')
        if not body.get('stream'):
            return self.send_json(200, response if self.path == '/v1/responses' else chat)
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Cache-Control', 'no-cache')
        self.end_headers()
        try:
            if self.path == '/v1/responses':
                for sequence, (name, payload) in enumerate(response_events(response)):
                    payload = dict(payload, type=name, sequence_number=sequence)
                    self.wfile.write(('event: ' + name + '\ndata: ' + json.dumps(payload) + '\n\n').encode())
                    self.wfile.flush()
            else:
                base = {'id': chat.get('id', response['id']), 'object': 'chat.completion.chunk', 'created': int(time.time()), 'model': request['model']}
                chunks = [dict(base, choices=[{'index': 0, 'delta': {'role': 'assistant', 'content': response['output'][0]['content'][0]['text']}, 'finish_reason': None}]),
                          dict(base, choices=[{'index': 0, 'delta': {}, 'finish_reason': chat['choices'][0]['finish_reason']}])]
                if body.get('stream_options', {}).get('include_usage'):
                    chunks.append(dict(base, choices=[], usage=chat.get('usage', {})))
                for chunk in chunks:
                    self.wfile.write(('data: ' + json.dumps(chunk) + '\n\n').encode())
                self.wfile.write(b'data: [DONE]\n\n')
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass


if __name__ == '__main__':
    adapter_key = os.environ.get('G2A_API_KEY', '')
    gemini_key = os.environ.get('GEMINI_API_KEY', '')
    models = [model.strip() for model in os.environ.get('G2A_MODELS', '').split(',') if model.strip()]
    if not adapter_key or not gemini_key or not models:
        raise SystemExit('G2A_API_KEY, GEMINI_API_KEY and G2A_MODELS are required')
    host, port = os.environ.get('G2A_LISTEN', '127.0.0.1:7877').rsplit(':', 1)
    upstream_proxy = os.environ.get('G2A_UPSTREAM_PROXY')
    Server((host, int(port)), adapter_key, gemini_key, models, upstream_proxy=upstream_proxy).serve_forever()
