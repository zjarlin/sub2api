"""Responses tool declarations and validated text decisions, without Chat messages."""
import json
import re
import uuid
from models import OutputFormat, ToolCallValidationError
from structured_output import json_object, validate_output, validate_schema
from custom_grammar import validate_format, validate_input


def name(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', value):
        raise ValueError('Invalid Responses tool name')
    return value


def identity(tool):
    return (tool.get('namespace', ''), tool['name'])


def parse_tools(declarations, choice=None, parallel=True):
    declarations = [] if declarations is None else declarations
    if not isinstance(declarations, list) or not isinstance(parallel, bool):
        raise ValueError('tools must be an array and parallel_tool_calls must be boolean')
    functions = {}

    def add(tool, namespace=''):
        if not isinstance(tool, dict):
            raise ValueError('Each tool must be an object')
        kind = tool.get('type')
        if kind == 'namespace' and not namespace:
            ns = name(tool.get('name'))
            children = tool.get('tools', tool.get('children'))
            if not isinstance(children, list):
                raise ValueError('Namespace tools must be an array')
            for child in children:
                add(child, ns)
            return
        if kind == 'tool_search':
            if namespace or tool.get('execution', 'client') != 'client':
                raise ValueError('Only client-executed tool_search is supported')
            tool = {**tool, 'name': 'tool_search', 'parameters': tool.get('parameters') or {
                'type': 'object', 'properties': {'query': {'type': 'string'}, 'limit': {'type': 'integer'}},
                'required': ['query']}}
        if kind not in ('function', 'custom', 'tool_search'):
            raise ValueError('Supported Responses tools: function, custom, namespace, client tool_search')
        entry = {**tool, 'name': name(tool.get('name'))}
        if namespace:
            entry['namespace'] = namespace
        elif tool.get('namespace'):
            entry['namespace'] = name(tool['namespace'])
        if not isinstance(tool.get('description', ''), str):
            raise ValueError('Tool description must be text')
        if kind in ('function', 'tool_search'):
            parameters = tool.get('parameters') or {'type': 'object', 'properties': {}}
            validate_schema(parameters, 'Function parameters')
            entry['parameters'] = parameters
        else:
            fmt = tool.get('format') or {'type': 'text'}
            validate_format(fmt)
        key = identity(entry)
        if key in functions:
            raise ValueError('Duplicate Responses tool identity')
        functions[key] = entry

    for tool in declarations:
        add(tool)
    choice = ('auto' if functions else 'none') if choice is None else choice
    if isinstance(choice, str):
        if choice not in ('none', 'auto', 'required') or (choice == 'required' and not functions):
            raise ValueError('Invalid tool_choice')
    elif isinstance(choice, dict):
        key = (choice.get('namespace', ''), choice.get('name'))
        if choice.get('type') not in ('function', 'custom', 'tool_search') or key not in functions:
            raise ValueError('tool_choice names an undeclared tool')
        if functions[key]['type'] != choice['type']:
            raise ValueError('tool_choice type does not match the declaration')
    else:
        raise ValueError('Invalid tool_choice')
    return {'tools': list(functions.values()), 'tool_choice': choice, 'parallel_tool_calls': parallel}


def prompt(text, policy, output_format, include_tools=True):
    from structured_output import format_prompt
    if not policy['tools'] or policy['tool_choice'] == 'none':
        return format_prompt(text, output_format)
    contract = dict(policy)
    if not include_tools:
        contract.pop('tools')
    instruction = (
        '\n客户端 Responses 工具协议：仅描述调用，由调用方执行并返回真实结果；不要执行豆包内置工具。'
        '只返回一个 JSON 对象：{"text":null,"calls":[{"type":"function","name":"函数名",'
        '"namespace":"声明的命名空间（无则省略）","arguments":{}}]}。'
        'custom 调用使用 type=custom 和 input 字符串，不使用 arguments。'
        'tool_search 使用 type=tool_search、name=tool_search 和 arguments 对象，由调用方返回工具定义。'
        '最终回答用 {"text":"回答","calls":[]}。禁止编造工具结果。'
        'required 必须调用工具，指定工具必须调用该工具一次，parallel_tool_calls=false 最多调用一次。'
        'arguments 必须符合 parameters。沿用未重复列出的工具定义。\n' +
        json.dumps(contract, ensure_ascii=False))
    if output_format.kind != 'text':
        instruction += '\n以下要求仅约束最终 text 字符串中的答案：' + format_prompt('', output_format)
    return text + instruction


def decode(text, policy, output_format):
    if not policy['tools'] or policy['tool_choice'] == 'none':
        return [message(validate_output(text, output_format))]
    try:
        decision = json_object(text)
        if set(decision) != {'text', 'calls'}:
            raise ValueError('Invalid decision fields')
        answer, calls = decision['text'], decision['calls']
        if answer is not None and not isinstance(answer, str):
            raise ValueError('Invalid answer')
        if not isinstance(calls, list) or len(calls) > 128:
            raise ValueError('Invalid calls')
        choice = policy['tool_choice']
        if (choice == 'required' or isinstance(choice, dict)) and not calls:
            raise ValueError('Required call missing')
        if (not policy['parallel_tool_calls'] or isinstance(choice, dict)) and len(calls) > 1:
            raise ValueError('Parallel calls prohibited')
        declared = {identity(tool): tool for tool in policy['tools']}
        output = [message(answer)] if answer else []
        for call in calls:
            if not isinstance(call, dict):
                raise ValueError('Invalid call')
            tool = declared.get(identity(call))
            if not tool or call.get('type') != tool['type']:
                raise ValueError('Unknown tool')
            if isinstance(choice, dict) and identity(call) != identity(choice):
                raise ValueError('Unselected tool')
            item = {'type': tool['type'] + '_call', 'id': 'fc_' + uuid.uuid4().hex,
                    'call_id': 'call_' + uuid.uuid4().hex, 'name': tool['name'], 'status': 'completed'}
            if tool.get('namespace'):
                item['namespace'] = tool['namespace']
            if tool['type'] in ('function', 'tool_search'):
                if set(call) - {'type', 'name', 'namespace', 'arguments'} or not isinstance(call.get('arguments'), dict):
                    raise ValueError('Invalid function arguments')
                item['arguments'] = validate_output(json.dumps(call['arguments'], allow_nan=False),
                                                    OutputFormat('json_schema', tool['parameters']))
                if tool['type'] == 'tool_search':
                    item['arguments'] = json.loads(item['arguments'])
                    item['execution'] = 'client'
                    item.pop('name')
            else:
                if set(call) - {'type', 'name', 'namespace', 'input'} or not isinstance(call.get('input'), str):
                    raise ValueError('Invalid custom input')
                item['type'] = 'custom_tool_call'
                item['input'] = call['input']
                validate_input(item['input'], tool.get('format'))
            output.append(item)
        if not calls:
            if not isinstance(answer, str):
                raise ValueError('Final answer missing')
            output = [message(validate_output(answer, output_format))]
        return output
    except (ValueError, TypeError, KeyError, RecursionError) as error:
        raise ToolCallValidationError('Desktop reply does not match the Responses tool policy') from error


def message(text):
    return {'type': 'message', 'id': 'msg_' + uuid.uuid4().hex, 'status': 'completed',
            'role': 'assistant', 'content': [{'type': 'output_text', 'text': text, 'annotations': []}]}
