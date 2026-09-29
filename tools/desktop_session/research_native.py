"""Server-side probes for observed desktop tool endpoints; never executes tools."""
import argparse
import json
import uuid
from pathlib import Path
from transport import read_json, request


def shape(value, depth=0):
    if depth > 4:
        return type(value).__name__
    if isinstance(value, dict):
        return {key: shape(item, depth + 1) for key, item in value.items()}
    if isinstance(value, list):
        return {'count': len(value), 'item': shape(value[0], depth + 1) if value else None}
    return type(value).__name__


def run(session_file, register=False, channel=False):
    context = json.loads(Path(session_file).read_text())
    cookies, params = context['cookies'], context['params']
    status, body = read_json(cookies, '/alice/dispatch/list_devices', {}, params=params)
    report = {'list_devices': {'http_status': status, 'business_code': body.get('code'), 'shape': shape(body)}}
    # Do not connect as one of the user's existing devices: it could receive
    # live desktop work. The probe owns this fresh identity and runs no tasks.
    identity = 'sub2api-probe-' + uuid.uuid4().hex
    if register:
        status, body = read_json(cookies, '/samantha/tool/init', {'client_id': identity, 'tools': [
            {'name': 'lookup_probe', 'description': 'Return a marker supplied by the API caller',
             'input_schema': {'type': 'object', 'properties': {}}}]}, params=params)
        report['legacy_tool_init'] = {'http_status': status, 'business_code': body.get('code'), 'shape': shape(body)}
    if channel:
        try:
            with request(cookies, '/alice/office/tool_local/chunk_stream',
                         {'device_id': identity, 'client_attrs': {}}, params=params, timeout=8) as response:
                report['isolated_chunk_stream'] = {'http_status': response.status,
                                                   'content_type': response.headers.get('Content-Type')}
                line = response.readline(16384).decode()
                # Only framing and JSON keys are reported, not command contents.
                report['isolated_chunk_stream']['first_line_kind'] = line.split(':', 1)[0][:32]
        except Exception as error:
            report['isolated_chunk_stream'] = {'outcome': type(error).__name__, 'timeout_seconds': 8}
    report['native_custom_tool_roundtrip_verified'] = False
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--session-file', default='/run/secrets/session.json')
    parser.add_argument('--register', action='store_true', help='Register one isolated legacy test tool')
    parser.add_argument('--channel', action='store_true', help='Observe an isolated device channel for up to 8 seconds')
    args = parser.parse_args()
    print(json.dumps(run(args.session_file, args.register, args.channel), ensure_ascii=False, indent=2))
