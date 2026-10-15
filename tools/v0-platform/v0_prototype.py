#!/usr/bin/env python3
"""通过 Sub2API 生成原型，将文件安全保存到运行客户端的电脑。"""
import argparse
import base64
import json
import os
from pathlib import Path, PurePosixPath
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile

MAX_BYTES = 64 * 1024 * 1024


def request(method, endpoint, payload=None):
    base = os.environ.get('SUB2API_BASE_URL', 'http://192.168.31.252:18080').rstrip('/')
    if base.endswith('/v1'):
        base = base[:-3]
    key = os.environ.get('SUB2API_API_KEY', '').strip()
    if not key:
        key_file = os.environ.get('SUB2API_API_KEY_FILE')
        if key_file:
            key = Path(key_file).expanduser().read_text().strip()
    if not key:
        raise ValueError('Set SUB2API_API_KEY or SUB2API_API_KEY_FILE to a gateway key bound to a v0/composite group')
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(base + '/v1/v0/' + endpoint, data=data, method=method,
                                 headers={'Authorization': 'Bearer ' + key, 'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=660) as response:
            body = response.read(MAX_BYTES + 1)
            if len(body) > MAX_BYTES:
                raise ValueError('Gateway response exceeds 64 MiB')
            return json.loads(body), response.headers.get('X-V0-Chat-Handle')
    except urllib.error.HTTPError as error:
        raise ValueError('Gateway HTTP ' + str(error.code) + ': ' + error.read(4096).decode(errors='replace')) from error


def save_files(payload, root):
    files = payload.get('files')
    if not isinstance(files, list) or not files:
        raise ValueError('v0 has not returned source files yet; retry v0_download with the returned handle')
    validated = []
    names = set()
    total = 0
    for item in files:
        name = item['path']
        path = PurePosixPath(name)
        if not name or path.is_absolute() or '..' in path.parts or '\\' in name or '\x00' in name or str(path) == '.':
            raise ValueError('Unsafe source path: ' + repr(name))
        name = str(path)
        if name in names:
            raise ValueError('Duplicate source path: ' + name)
        names.add(name)
        encoding = item.get('encoding', 'utf8')
        if encoding == 'utf8':
            content = item['content'].encode('utf-8')
        elif encoding == 'base64':
            content = base64.b64decode(item['content'], validate=True)
        else:
            raise ValueError('Unsupported source encoding: ' + str(encoding))
        total += len(content)
        if total > MAX_BYTES:
            raise ValueError('Source exceeds 64 MiB')
        validated.append((name, content))
    for name in names:
        if any(str(parent) in names for parent in PurePosixPath(name).parents if str(parent) != '.'):
            raise ValueError('Source file conflicts with a directory: ' + name)
    root = Path(root).expanduser().resolve()
    root.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix='v0-', dir=str(root)))
    for name, content in validated:
        target = directory / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
    archive = directory.with_suffix('.zip')
    with zipfile.ZipFile(archive, 'x', zipfile.ZIP_DEFLATED) as output:
        for name, _ in validated:
            output.write(directory / name, arcname=name)
    return {'directory': str(directory), 'archive': str(archive), 'files': [name for name, _ in validated]}


def invoke(name, args):
    root = os.environ.get('V0_OUTPUT_DIR', str(Path.home() / 'prototypes'))
    if name == 'v0_download':
        handle = args['handle']
        files, _ = request('GET', 'chats/' + urllib.parse.quote(handle, safe='') + '/files')
        result = save_files(files, root)
        result['handle'] = handle
        return result
    if name == 'v0_create':
        payload = {'message': args['prompt'], 'privacy': 'private'}
        if args.get('framework'):
            payload['systemPrompt'] = 'Generate a runnable project using ' + args['framework'] + '. Include package.json and all necessary source files.'
        chat, handle = request('POST', 'chats', payload)
    elif name == 'v0_followup':
        handle = args['handle']
        chat, _ = request('POST', 'chats/' + urllib.parse.quote(handle, safe='') + '/messages', {'message': args['prompt']})
    else:
        raise ValueError('Unknown tool: ' + name)
    if not handle:
        raise ValueError('Gateway did not return a v0 chat handle')
    result = {'handle': handle, 'chat': chat}
    try:
        result.update(invoke('v0_download', {'handle': handle}))
    except (ValueError, OSError, urllib.error.URLError) as error:
        result['download_error'] = str(error)
    return result


TOOLS = [
    {'name': 'v0_create', 'description': 'Generate a website or UI prototype with v0 via Sub2API and download its source and ZIP onto this computer. Uses paid v0 credits.',
     'inputSchema': {'type': 'object', 'properties': {'prompt': {'type': 'string'}, 'framework': {'type': 'string', 'description': 'For example React, Vue 3 + Vite'}}, 'required': ['prompt'], 'additionalProperties': False}},
    {'name': 'v0_followup', 'description': 'Modify an existing v0 prototype and download a new local copy. Uses paid v0 credits.',
     'inputSchema': {'type': 'object', 'properties': {'handle': {'type': 'string'}, 'prompt': {'type': 'string'}}, 'required': ['handle', 'prompt'], 'additionalProperties': False}},
    {'name': 'v0_download', 'description': 'Download a generated v0 project as local files and ZIP. Use if generation finished but the initial file download was not ready.',
     'inputSchema': {'type': 'object', 'properties': {'handle': {'type': 'string'}}, 'required': ['handle'], 'additionalProperties': False}}
]


def serve():
    for line in sys.stdin:
        message = json.loads(line)
        if 'id' not in message:
            continue
        method = message.get('method')
        if method == 'initialize':
            result = {'protocolVersion': '2024-11-05', 'capabilities': {'tools': {}}, 'serverInfo': {'name': 'sub2api-v0-local', 'version': '1.0.0'}}
        elif method == 'ping':
            result = {}
        elif method == 'tools/list':
            result = {'tools': TOOLS}
        elif method == 'tools/call':
            try:
                value = invoke(message['params']['name'], message['params'].get('arguments', {}))
                result = {'content': [{'type': 'text', 'text': json.dumps(value, ensure_ascii=False)}]}
            except Exception as error:
                result = {'isError': True, 'content': [{'type': 'text', 'text': str(error)}]}
        else:
            print(json.dumps({'jsonrpc': '2.0', 'id': message['id'], 'error': {'code': -32601, 'message': 'Method not found'}}), flush=True)
            continue
        print(json.dumps({'jsonrpc': '2.0', 'id': message['id'], 'result': result}), flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mcp', action='store_true')
    parser.add_argument('tool', nargs='?', choices=['v0_create', 'v0_followup', 'v0_download'])
    parser.add_argument('--prompt')
    parser.add_argument('--framework')
    parser.add_argument('--handle')
    args = parser.parse_args()
    if args.mcp:
        serve()
    elif args.tool:
        print(json.dumps(invoke(args.tool, vars(args)), ensure_ascii=False, indent=2))
    else:
        parser.print_help()
