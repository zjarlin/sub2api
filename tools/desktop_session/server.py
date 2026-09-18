"""Authenticated local adapter. Serialize generation and buffer until upstream verification."""
import hmac
import json
import os
import threading
import time
import traceback
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from api import completion, encode_stream, parse_request
from client import DesktopClient
from conversations import ConversationStore
from reply import UpstreamError
from structured_output import OutputValidationError
from tool_calls import ToolCallValidationError

DEFAULT_MAX_REQUEST_BODY_BYTES = 32 * 1024 * 1024
CAPACITY_COOLDOWN_SECONDS = 30
UPSTREAM_MESSAGES = {
    'upstream_capacity': 'Doubao desktop capacity is temporarily unavailable; retry after 30 seconds',
    'upstream_timeout': 'Doubao desktop generation timed out before a complete reply',
    'upstream_connection_failed': 'Connection to Doubao desktop upstream failed',
}


class Adapter:
    def __init__(self, session_file, key_file, max_request_body_bytes=DEFAULT_MAX_REQUEST_BODY_BYTES,
                 state_file=None):
        self.max_request_body_bytes = int(max_request_body_bytes)
        if self.max_request_body_bytes <= 0:
            raise ValueError('DESKTOP_MAX_REQUEST_BODY_BYTES must be positive')
        self.session_file = Path(session_file)
        self.key = Path(key_file).read_text().strip()
        if len(self.key) < 32:
            raise ValueError('Adapter API key must have at least 32 characters')
        self.lock = threading.Lock()
        self.capacity_until = {}
        self.conversations = ConversationStore(state_file)
        self.client, self.version, self.blocked = None, None, False
        self.reload()

    def reload(self):
        version = self.session_file.stat().st_mtime_ns
        if self.version != version:
            raw = self.session_file.read_bytes()
            if len(raw) > 65536:
                raise ValueError('Session snapshot exceeded limit')
            context = json.loads(raw)
            self.client = DesktopClient(context)
            self.conversations.bind_session(context)
            self.version, self.blocked = version, False
            self.capacity_until.clear()

    def complete(self, request, scope=''):
        model = self.client.resolve_model(request.model)['id']
        if time.monotonic() < self.capacity_until.get(model, 0):
            raise UpstreamError('upstream_capacity', 503)
        plan = self.conversations.plan(request, model, scope)
        if plan.cached is not None:
            return plan.cached
        try:
            if plan.cursor is None:
                reply = self.client.complete(plan.text, model)
            else:
                reply = self.client.complete(plan.text, model, cursor=plan.cursor)
        except UpstreamError as error:
            if error.code == 'upstream_capacity':
                self.capacity_until[model] = time.monotonic() + CAPACITY_COOLDOWN_SECONDS
            raise
        result = completion(model, reply, request.output_format, request.tools)
        self.conversations.commit(request, model, scope, reply, result, plan)
        return result


def handler_for(adapter):
    class Handler(BaseHTTPRequestHandler):
        def setup(self):
            super().setup()
            self.connection.settimeout(15)

        def log_message(self, *args):
            pass

        def respond(self, status, body, content_type='application/json; charset=utf-8'):
            # 状态处理已结束；先释放生成锁，收到响应后立即续聊不会误报并发。
            if getattr(self, 'owns_lock', False):
                self.owns_lock = False
                adapter.lock.release()
            self.response_status = status
            raw = body if isinstance(body, bytes) else json.dumps(body, ensure_ascii=False).encode()
            self.send_response(status)
            self.send_header('Content-Type', content_type)
            self.send_header('Content-Length', str(len(raw)))
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Connection', 'close')
            if getattr(self, 'request_id', None):
                self.send_header('X-Request-ID', self.request_id)
            if getattr(self, 'error_code', '') == 'upstream_capacity':
                self.send_header('Retry-After', str(CAPACITY_COOLDOWN_SECONDS))
            self.end_headers()
            self.close_connection = True
            self.wfile.write(raw)

        def error(self, status, code, message):
            self.error_code = str(code)
            self.respond(status, {'error': {'type': 'desktop_adapter_error',
                                           'code': str(code), 'message': message}})

        def diagnostic(self, error, phase, started, model):
            record = {'event': 'desktop_request', 'request_id': self.request_id,
                      'phase': phase, 'model': model, 'status': getattr(self, 'response_status', None),
                      'code': getattr(self, 'error_code', None),
                      'elapsed_ms': round((time.monotonic() - started) * 1000)}
            if error is not None:
                # 异常文本、源码行和请求内容可能含凭据，只记录类型与栈位置。
                cause = error.__cause__ or error
                record.update(exception=type(error).__name__, cause=type(cause).__name__,
                              frames=[{'file': Path(frame.filename).name, 'line': frame.lineno,
                                       'function': frame.name}
                                      for frame in traceback.extract_tb(cause.__traceback__)[-8:]])
            print(json.dumps(record, ensure_ascii=False), flush=True)

        def authorize(self):
            if hmac.compare_digest(self.headers.get('Authorization', ''), 'Bearer ' + adapter.key):
                return True
            self.error(401, 'unauthorized', 'A valid adapter Bearer token is required')
            return False

        def do_GET(self):
            if self.path == '/health':
                self.respond(200, {'status': 'verification_required' if adapter.blocked else 'ready',
                                   'needs_verification': adapter.blocked})
            elif self.authorize():
                if self.path == '/v1/models':
                    self.respond(200, {'object': 'list', 'data': [
                        {'id': model['id'], 'object': 'model', 'created': 0, 'owned_by': 'doubao-desktop',
                         'name': model['name']}
                        for model in adapter.client.models.values()]})
                else:
                    self.error(404, 'not_found', 'Endpoint not supported')

        def do_POST(self):
            self.request_id = uuid.uuid4().hex
            if not self.authorize():
                return
            if self.path != '/v1/chat/completions':
                self.error(404, 'not_found', 'Use /v1/chat/completions')
                return
            if not adapter.lock.acquire(blocking=False):
                self.error(429, 'desktop_busy', 'One request is already running')
                return
            started, phase, model, failure = time.monotonic(), 'request', None, None
            self.owns_lock = True
            try:
                if self.headers.get('Transfer-Encoding') is not None:
                    raise ValueError('Transfer-Encoding is not supported; send Content-Length')
                if len(self.headers.get_all('Content-Length', [])) != 1:
                    raise ValueError('Send exactly one Content-Length header')
                length = int(self.headers.get('Content-Length', '0'))
                if length <= 0:
                    raise ValueError('Send a non-empty JSON body')
                if length > adapter.max_request_body_bytes:
                    self.error(413, 'request_too_large',
                               f'Send a JSON body of at most {adapter.max_request_body_bytes} bytes')
                    return
                raw = self.rfile.read(length)
                if len(raw) != length:
                    raise ValueError('Request body does not match Content-Length')
                request = parse_request(json.loads(raw))
                model = adapter.client.resolve_model(request.model)['id']
                phase = 'session'
                adapter.reload()
                if adapter.blocked:
                    raise UpstreamError(710022004, 503)
                scope = (self.headers.get('X-Desktop-Session') or self.headers.get('session_id') or
                         self.headers.get('conversation_id') or '')
                if len(scope) > 256:
                    phase = 'request'
                    raise ValueError('Session identifier must contain at most 256 characters')
                phase = 'generation'
                result = adapter.complete(request, scope)
                phase = 'response'
                if request.stream:
                    self.respond(200, encode_stream(result), 'text/event-stream; charset=utf-8')
                else:
                    self.respond(200, result)
            except ToolCallValidationError as error:
                failure = error
                self.error(502, 'invalid_tool_calls', str(error))
            except OutputValidationError as error:
                failure = error
                self.error(502, 'invalid_response_format', str(error))
            except (ValueError, TypeError) as error:
                failure = error
                if phase == 'request':
                    self.error(400, 'invalid_request', str(error) if isinstance(error, ValueError) else 'Invalid request')
                else:
                    self.error(502, 'desktop_internal_error', 'Desktop adapter could not process the upstream reply')
            except UpstreamError as error:
                failure = error
                if error.code == '710022004':
                    adapter.blocked = True
                self.error(error.status, error.code, 'Desktop verification required; refresh session' if
                           adapter.blocked else UPSTREAM_MESSAGES.get(error.code, 'Desktop reply failed validation'))
            except (BrokenPipeError, ConnectionResetError) as error:
                failure = error
                self.error_code = 'client_disconnected'
            except Exception as error:
                failure = error
                self.error(502, 'upstream_unavailable', 'Desktop session or upstream request unavailable')
            finally:
                try:
                    self.diagnostic(failure, phase, started, model)
                finally:
                    if self.owns_lock:
                        adapter.lock.release()
    return Handler


if __name__ == '__main__':
    adapter = Adapter(os.environ.get('DOUBAO_SESSION_FILE', '/run/secrets/session.json'),
                      os.environ.get('DOUBAO_API_KEY_FILE', '/run/secrets/api_key'),
                      os.environ.get('DESKTOP_MAX_REQUEST_BODY_BYTES', DEFAULT_MAX_REQUEST_BODY_BYTES),
                      os.environ.get('DESKTOP_STATE_FILE'))
    server = ThreadingHTTPServer(('0.0.0.0', 8080), handler_for(adapter))
    server.daemon_threads = True
    print('Desktop subscription adapter listening on :8080', flush=True)
    server.serve_forever()
