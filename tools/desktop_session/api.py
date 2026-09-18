import json
import time
import uuid
from messages import parse_messages
from models import ChatRequest
from structured_output import parse_response_format, validate_output
from tool_calls import decode_tool_reply, tool_prompt
from tool_policy import parse_tools


def parse_request(body):
    if not isinstance(body, dict):
        raise ValueError('Expected a JSON object')
    unsupported = set(body) - {'model', 'messages', 'stream', 'stream_options', 'n', 'response_format',
                               'tools', 'tool_choice', 'parallel_tool_calls'}
    if unsupported:
        raise ValueError('Unsupported parameters: ' + ', '.join(sorted(unsupported)))
    if body.get('n', 1) != 1 or not isinstance(body.get('stream', False), bool):
        raise ValueError('Only n=1 and boolean stream are supported')
    if not isinstance(body.get('model'), str):
        raise ValueError('Use a desktop model ID from /v1/models')
    messages = parse_messages(body.get('messages'))
    tools = parse_tools(body)
    plain_user = len(messages) == 1 and messages[0]['role'] == 'user' and messages[0]['content'].strip()
    text = messages[0]['content'] if plain_user else (
        '以下 JSON 是本次对话的完整上下文，请依据角色、内容和已返回的工具结果生成下一条助手回复。'
        '若末条消息是 assistant，请继续其尚未完成的回答或任务，不要将其当作用户消息，也不要重复已有内容。'
        '不要执行豆包内置工具。\n' + json.dumps(messages, ensure_ascii=False))
    output_format = parse_response_format(body.get('response_format'))
    return ChatRequest(body['model'], tool_prompt(text, tools, output_format), body.get('stream', False),
                       output_format, tools, messages)


def completion(model, reply, output_format=None, tools=None):
    if tools is not None and tools.enabled:
        message, finish_reason = decode_tool_reply(reply.text, tools, output_format)
    else:
        message = {'role': 'assistant', 'content': validate_output(reply.text, output_format)}
        finish_reason = 'stop'
    return {'id': 'chatcmpl-' + uuid.uuid4().hex, 'object': 'chat.completion',
            'created': int(time.time()), 'model': model,
            'choices': [{'index': 0, 'message': message, 'finish_reason': finish_reason}]}


def encode_stream(result):
    common = {key: result[key] for key in ('id', 'created', 'model')}
    common['object'] = 'chat.completion.chunk'
    choice = result['choices'][0]
    delta = dict(choice['message'])
    if 'tool_calls' in delta:
        delta['tool_calls'] = [{**call, 'index': index} for index, call in enumerate(delta['tool_calls'])]
    choices = [{'index': 0, 'delta': delta, 'finish_reason': None},
               {'index': 0, 'delta': {}, 'finish_reason': choice['finish_reason']}]
    return ''.join('data: ' + json.dumps({**common, 'choices': [choice]}, ensure_ascii=False) + '\n\n'
                   for choice in choices).encode() + b'data: [DONE]\n\n'
