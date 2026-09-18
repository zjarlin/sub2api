"""通过 HTTP 验证同一桌面会话的工具往返、重试和多轮增量输入。"""
import argparse
import json
import uuid
from pathlib import Path
from smoke import failure, request_reply
from smoke_tools import read_choice


def response_id(raw, stream):
    if not stream:
        return json.loads(raw)['id']
    first = next(line[6:] for line in raw.splitlines() if line.startswith('data: {'))
    return json.loads(first)['id']


def exchange(body, state_file, gateway):
    status, raw, size = request_reply(body, gateway)
    stream = body.get('stream', False)
    choice = read_choice(raw, stream, body['model'])
    identifier = response_id(raw, stream)
    entries = json.loads(Path(state_file).read_text())['entries']
    entry = next((entry for key, entry in entries if entry['result']['id'] == identifier), None)
    if entry is None:
        raise ValueError('HTTP reply has no committed conversation state')
    metrics = {'http_status': status, 'continued': entry['continued'],
               'upstream_input_bytes': entry['input_bytes']}
    print(json.dumps(metrics), flush=True)
    return choice, entry['cursor']['conversation_id'], identifier


def run(state_file, model='doubao-pro', stream=False, gateway=False, plain_text=False):
    answer_instruction = ('只回复标记本身，不要额外文字。' if plain_text else
                          '仅返回 JSON 对象，字段 marker 为实际标记。')
    body = {'model': model, 'stream': stream,
            'messages': [{'role': 'system', 'content': '严格遵守客户端工具协议。以下填充内容可忽略：' + 'x' * 70000},
                         {'role': 'user', 'content': '调用 lookup_marker 获取 alpha 的随机标记。收到结果后' +
                          answer_instruction}],
            'tools': [{'type': 'function', 'function': {
                'name': 'lookup_marker', 'description': '读取客户端生成的未知随机标记',
                'parameters': {'type': 'object', 'properties': {'key': {'type': 'string', 'enum': ['alpha']}},
                               'required': ['key'], 'additionalProperties': False}}}],
            'tool_choice': 'required', 'response_format': {'type': 'json_object'}}
    if plain_text:
        body.pop('response_format')
    first, conversation, identifier = exchange(body, state_file, gateway)
    calls = first['message'].get('tool_calls', [])
    if first['finish_reason'] != 'tool_calls' or len(calls) != 1:
        raise ValueError('Expected one real function decision')
    call = calls[0]
    if call['function']['name'] != 'lookup_marker' or json.loads(call['function']['arguments']) != {'key': 'alpha'}:
        raise ValueError('Unexpected function or arguments')
    expected = {'marker': 'CONT_RESULT_' + uuid.uuid4().hex}
    def matches(choice):
        content = choice['message']['content']
        return content.strip() == expected['marker'] if plain_text else json.loads(content) == expected
    body['messages'].extend([first['message'], {'role': 'tool', 'tool_call_id': call['id'],
                                               'content': json.dumps(expected)}])
    body['tool_choice'] = 'auto'
    second, continued, identifier = exchange(body, state_file, gateway)
    if continued != conversation or not matches(second):
        raise ValueError('Tool result was not continued in the same conversation')
    replay, repeated, replay_id = exchange(body, state_file, gateway)
    if replay_id != identifier or repeated != conversation or replay != second:
        raise ValueError('Retry did not replay the confirmed response')
    body['messages'].extend([second['message'], {'role': 'user', 'content':
                            '不要调用工具。再次返回之前工具结果中的 marker。' + answer_instruction}])
    third, continued, identifier = exchange(body, state_file, gateway)
    if continued != conversation or not matches(third):
        raise ValueError('Third turn did not retain the tool result')
    return {'model': model, 'stream': stream, 'through_sub2api': gateway, 'plain_text': plain_text,
            'same_conversation': True, 'tool_result_verified': True, 'retry_replayed': True,
            'three_turns_verified': True}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-file', required=True)
    parser.add_argument('--model', default='doubao-pro')
    parser.add_argument('--stream', action='store_true')
    parser.add_argument('--gateway', action='store_true')
    parser.add_argument('--plain-text', action='store_true')
    args = parser.parse_args()
    try:
        result = run(args.state_file, args.model, args.stream, args.gateway, args.plain_text)
        success = True
    except Exception as error:
        result, success = failure(error), False
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(0 if success else 1)
