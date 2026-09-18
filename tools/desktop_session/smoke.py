"""One synthetic generation through the local adapter or the configured Sub2API group."""
import argparse
import json
import os
import urllib.error
import urllib.request
import uuid
from pathlib import Path


def request_reply(body, gateway=False):
    runtime = Path(__file__).resolve().parent / 'runtime'
    if gateway:
        access = json.loads((runtime / 'sub2api-access.json').read_text())
        url, key = access['base_url'], access['api_key']
    else:
        url = os.environ.get('DESKTOP_ADAPTER_BASE_URL', 'http://127.0.0.1:18089/v1')
        key = Path(os.environ.get('DOUBAO_API_KEY_FILE', runtime / 'api_key')).read_text().strip()
    request_body = json.dumps(body).encode()
    request = urllib.request.Request(url + '/chat/completions', data=request_body,
                                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key})
    with urllib.request.urlopen(request, timeout=110) as response:
        return response.status, response.read(1048576).decode(), len(request_body)


def failure(error):
    result = {'error_type': type(error).__name__, 'http_status': getattr(error, 'code', None)}
    if isinstance(error, urllib.error.HTTPError):
        try:
            payload = json.loads(error.read(4096))
            detail = payload.get('error', payload)
            result.update(error_code=detail.get('code'), error_message=detail.get('message'))
        except (ValueError, AttributeError):
            pass
    return result


def run(gateway=False, model='doubao-pro', stream=False, response_format=None, padding_bytes=0, tail_role=None):
    if padding_bytes < 0:
        raise ValueError('padding_bytes must be non-negative')
    nonce = 'DESKTOP_SMOKE_' + uuid.uuid4().hex[:12]
    structured = response_format in ('json_object', 'json_schema')
    prompt = ('不要调用工具，只返回 JSON 对象，唯一字段 marker 的值为：' if structured else
              '不要调用工具，只回复原文：') + nonce
    if padding_bytes:
        prompt = '忽略以下填充文本。\n<填充>' + 'x' * padding_bytes + '</填充>\n' + prompt
    body = {'model': model, 'stream': stream, 'messages': [{'role': 'user', 'content': prompt}]}
    if tail_role is not None:
        content = '正在处理上文的请求。' if tail_role == 'assistant' else '请完成上文的请求。'
        body['messages'].append({'role': tail_role, 'content': '' if tail_role == 'user' else content})
    if response_format is not None:
        body['response_format'] = {'type': response_format}
    if response_format == 'json_schema':
        body['response_format']['json_schema'] = {
            'name': 'smoke_result', 'strict': True,
            'schema': {'type': 'object', 'properties': {'marker': {'type': 'string', 'enum': [nonce]}},
                       'required': ['marker'], 'additionalProperties': False}}
    status, raw, request_bytes = request_reply(body, gateway)
    if stream:
        events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith('data: {')]
        text = ''.join(choice.get('delta', {}).get('content', '')
                       for event in events for choice in event.get('choices', []))
        finished = 'data: [DONE]' in raw and any(
            choice.get('finish_reason') == 'stop' for event in events for choice in event.get('choices', []))
        model_matches = bool(events) and all(event.get('model') == model for event in events)
    else:
        result = json.loads(raw)
        choice = result['choices'][0]
        model_matches = result.get('model') == model
        text, finished = choice['message']['content'], choice['finish_reason'] == 'stop'
    matches = json.loads(text) == {'marker': nonce} if structured else text.strip() == nonce
    return {'http_status': status, 'through_sub2api': gateway, 'model': model, 'stream': stream,
            'response_format': response_format, 'tail_role': tail_role, 'request_bytes': request_bytes,
            'assistant_exact_match': matches, 'finished': finished,
            'response_model_matches': model_matches}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--gateway', action='store_true')
    parser.add_argument('--model', default='doubao-pro')
    parser.add_argument('--stream', action='store_true')
    parser.add_argument('--response-format', choices=('text', 'json_object', 'json_schema'))
    parser.add_argument('--padding-bytes', type=int, default=0)
    parser.add_argument('--tail-role', choices=('assistant', 'system', 'developer', 'user'))
    args = parser.parse_args()
    try:
        result = run(args.gateway, args.model, args.stream, args.response_format, args.padding_bytes, args.tail_role)
        success = result['assistant_exact_match'] and result['finished'] and result['response_model_matches']
    except Exception as error:
        result, success = failure(error), False
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(0 if success else 1)
