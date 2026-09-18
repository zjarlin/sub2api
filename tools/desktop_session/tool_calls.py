import json
import uuid
from models import OutputFormat
from structured_output import format_prompt, json_object, validate_output


class ToolCallValidationError(ValueError):
    pass


def tool_prompt(text, policy, output_format, include_functions=True):
    if not policy.enabled:
        return format_prompt(text, output_format)
    contract = {'tool_choice': policy.forced_name or policy.choice,
                'parallel_tool_calls': policy.parallel}
    if include_functions:
        contract['tools'] = list(policy.functions.values())
    instruction = (
        '\n\n客户端函数调用协议：不要执行任何函数，也不要使用豆包内置工具。'
        '你只能描述调用，由客户端实际执行并回传 role=tool 结果，不能编造执行结果。'
        '\n只输出一个 JSON 对象，必须含 content 和 tool_calls 两个字段，不要代码围栏。'
        '\n需要工具时：{"content":null,"tool_calls":[{"name":"函数名","arguments":{"参数名":"值"}}]}。'
        'arguments 必须是符合该函数 parameters 的 JSON 对象。'
        '\n不需要工具时：{"content":"最终回答","tool_calls":[]}。'
        '只有客户端提供了相应工具结果，才能把它用于最终回答。'
        '\nrequired 必须至少调用一个函数，指定函数名时只能调用该函数一次；'
        'auto 可选择调用或回答；parallel_tool_calls=false 时最多一次调用。' +
        ('\n可用函数和本轮调用策略：\n' if include_functions else
         '\n沿用本会话已有的函数定义。本轮调用策略：\n') + json.dumps(contract, ensure_ascii=False))
    if output_format.kind != 'text':
        instruction += '\n以下格式要求仅用于无调用时 content 字符串内的最终答案：' + format_prompt('', output_format)
        instruction += '\n将该最终 JSON 答案编码为 content 的字符串值，外层仍为 content/tool_calls 对象。'
    return text + instruction


def decode_call(call, policy):
    if not isinstance(call, dict) or set(call) != {'name', 'arguments'}:
        raise ValueError('Invalid function call shape')
    name = call['name']
    if not isinstance(name, str) or name not in policy.functions or (policy.forced_name and name != policy.forced_name):
        raise ValueError('Unselected function')
    arguments = call['arguments']
    if not isinstance(arguments, dict):
        raise ValueError('Function arguments must be a JSON object')
    schema = policy.functions[name]['parameters']
    serialized = validate_output(json.dumps(arguments, allow_nan=False), OutputFormat('json_schema', schema))
    return {'id': 'call_' + uuid.uuid4().hex, 'type': 'function',
            'function': {'name': name, 'arguments': serialized}}


def decode_tool_reply(text, policy, output_format):
    try:
        decision = json_object(text)
        if set(decision) != {'content', 'tool_calls'}:
            raise ValueError('Invalid tool decision shape')
        content, calls = decision['content'], decision['tool_calls']
        if content is not None and not isinstance(content, str):
            raise ValueError('Tool decision content must be text or null')
        if not isinstance(calls, list) or len(calls) > 128:
            raise ValueError('Invalid tool call list')
        if not policy.parallel or policy.forced_name:
            if len(calls) > 1:
                raise ValueError('Multiple tool calls are not allowed')
        if policy.choice == 'required' and not calls:
            raise ValueError('Required tool call missing')
        if calls:
            return {'role': 'assistant', 'content': content,
                    'tool_calls': [decode_call(call, policy) for call in calls]}, 'tool_calls'
        if not isinstance(content, str):
            raise ValueError('Final answer must contain text')
    except (ValueError, TypeError, RecursionError) as error:
        raise ToolCallValidationError('Desktop reply does not match the requested tool policy or argument schema') from error
    return {'role': 'assistant', 'content': validate_output(content, output_format)}, 'stop'
