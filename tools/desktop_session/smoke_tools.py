import argparse
import json
import uuid
from smoke import failure, request_reply


def read_choice(raw, stream, model):
    if not stream:
        result = json.loads(raw)
        if result.get('model') != model:
            raise ValueError('Response model mismatch')
        return result['choices'][0]
    if not raw.endswith('data: [DONE]\n\n'):
        raise ValueError('SSE did not finish')
    events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith('data: {')]
    if not events or any(event.get('model') != model for event in events):
        raise ValueError('SSE model mismatch')
    content, calls, finish_reason = '', {}, None
    for event in events:
        choice = event['choices'][0]
        delta = choice.get('delta', {})
        content += delta.get('content') or ''
        finish_reason = choice.get('finish_reason') or finish_reason
        for part in delta.get('tool_calls', []):
            index = part['index']
            call = calls.setdefault(index, {'id': '', 'type': 'function', 'function': {'name': '', 'arguments': ''}})
            call['id'] += part.get('id', '')
            call['function']['name'] += part.get('function', {}).get('name', '')
            call['function']['arguments'] += part.get('function', {}).get('arguments', '')
    message = {'role': 'assistant', 'content': content or None}
    if calls:
        if sorted(calls) != list(range(len(calls))):
            raise ValueError('Invalid SSE tool indices')
        message['tool_calls'] = [calls[index] for index in sorted(calls)]
    return {'message': message, 'finish_reason': finish_reason}


def run(gateway=False, model='doubao-pro', stream=False, parallel=False, assistant_tail=False):
    keys = ['alpha', 'beta'] if parallel else ['alpha']
    function = {'name': 'lookup_marker', 'description': '从客户端读取指定 key 对应的未知随机标记',
                'parameters': {'type': 'object', 'properties': {'key': {'type': 'string', 'enum': keys}},
                               'required': ['key'], 'additionalProperties': False}, 'strict': True}
    prompt = ('先调用 lookup_marker 分别查询以下每个 key，不能猜测结果：' + ','.join(keys) +
              '。收到所有工具结果后，返回 JSON 对象 {"markers":[实际标记]}，标记顺序与 key 顺序一致。')
    body = {'model': model, 'stream': stream, 'messages': [{'role': 'user', 'content': prompt}],
            'tools': [{'type': 'function', 'function': function}], 'parallel_tool_calls': parallel,
            'tool_choice': 'required' if parallel else {'type': 'function', 'function': {'name': 'lookup_marker'}},
            'response_format': {'type': 'json_object'}}
    status, raw, request_bytes = request_reply(body, gateway)
    choice = read_choice(raw, stream, model)
    calls = choice['message'].get('tool_calls', [])
    if choice['finish_reason'] != 'tool_calls' or len(calls) != len(keys):
        raise ValueError('Expected function calls were not returned')
    if len({call['id'] for call in calls}) != len(calls) or any(not call['id'] for call in calls):
        raise ValueError('Missing or duplicate call IDs')
    arguments = [json.loads(call['function']['arguments']) for call in calls]
    if any(call['function']['name'] != 'lookup_marker' for call in calls):
        raise ValueError('Unexpected function selected')
    if sorted(argument.get('key') for argument in arguments) != keys or any(set(argument) != {'key'} for argument in arguments):
        raise ValueError('Function arguments differ from the request')
    first = {'http_status': status, 'phase': 'tool_calls', 'request_bytes': request_bytes, 'call_count': len(calls)}
    print(json.dumps(first), flush=True)
    markers = {key: 'TOOL_RESULT_' + uuid.uuid4().hex[:16] for key in keys}
    body['messages'].append(choice['message'])
    for call, argument in zip(calls, arguments):
        body['messages'].append({'role': 'tool', 'tool_call_id': call['id'],
                                 'content': json.dumps({'marker': markers[argument['key']]})})
    if assistant_tail:
        body['messages'].append({'role': 'assistant', 'content': '工具结果已收到，正在整理最终答案。'})
    body['tool_choice'] = 'auto'
    status, raw, request_bytes = request_reply(body, gateway)
    choice = read_choice(raw, stream, model)
    expected = {'markers': [markers[key] for key in keys]}
    if choice['finish_reason'] != 'stop' or json.loads(choice['message']['content']) != expected:
        raise ValueError('Tool result continuation did not return the actual markers')
    return {'http_status': status, 'phase': 'tool_results', 'through_sub2api': gateway, 'model': model,
            'stream': stream, 'parallel_tool_calls': parallel, 'assistant_tail': assistant_tail,
            'request_bytes': request_bytes,
            'call_count': len(calls), 'assistant_exact_match': True, 'finished': True}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Verify function calls and client-supplied result continuation')
    parser.add_argument('--gateway', action='store_true')
    parser.add_argument('--model', default='doubao-pro')
    parser.add_argument('--stream', action='store_true')
    parser.add_argument('--parallel', action='store_true')
    parser.add_argument('--assistant-tail', action='store_true')
    args = parser.parse_args()
    try:
        result, success = run(args.gateway, args.model, args.stream, args.parallel, args.assistant_tail), True
    except Exception as error:
        result, success = failure(error), False
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(0 if success else 1)
