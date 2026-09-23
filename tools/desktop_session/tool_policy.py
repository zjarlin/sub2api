import re
from models import ToolPolicy
from structured_output import validate_schema


def valid_name(value):
    return isinstance(value, str) and re.fullmatch(r'[A-Za-z0-9_-]{1,64}', value) is not None


def parse_function(tool):
    if not isinstance(tool, dict) or set(tool) != {'type', 'function'} or tool['type'] != 'function':
        raise ValueError('Only function tools are supported')
    function = tool['function']
    if not isinstance(function, dict) or set(function) - {'name', 'description', 'parameters', 'strict'}:
        raise ValueError('Invalid function tool fields')
    if not valid_name(function.get('name')):
        raise ValueError('Function names must contain 1 to 64 letters, digits, _ or -')
    if not isinstance(function.get('description', ''), str):
        raise ValueError('Function description must be text')
    if function.get('strict') is not None and not isinstance(function['strict'], bool):
        raise ValueError('Function strict must be boolean')
    parameters = function.get('parameters', {'type': 'object', 'properties': {}, 'additionalProperties': False})
    if isinstance(parameters, dict):
        parameters = {'type': 'object', **parameters}
    validate_schema(parameters, 'Function parameters')
    return {**function, 'parameters': parameters}


def parse_choice(value, functions):
    if value is None:
        return ('auto' if functions else 'none'), None
    if isinstance(value, str) and value in ('none', 'auto', 'required'):
        if value == 'required' and not functions:
            raise ValueError('tool_choice required needs at least one function')
        return value, None
    if (not isinstance(value, dict) or set(value) != {'type', 'function'} or
            value['type'] != 'function' or not isinstance(value['function'], dict) or
            set(value['function']) != {'name'}):
        raise ValueError('tool_choice must be none, auto, required or a named function')
    name = value['function']['name']
    if not valid_name(name) or name not in functions:
        raise ValueError('tool_choice names an undeclared function')
    return 'required', name


def parse_tools(body):
    tools = body.get('tools')
    tools = [] if tools is None else tools
    if not isinstance(tools, list) or len(tools) > 128:
        raise ValueError('tools must be an array of at most 128 functions')
    functions = {}
    for tool in tools:
        function = parse_function(tool)
        name = function['name']
        if name in functions:
            raise ValueError('Function names must be unique')
        functions[name] = function
    choice, forced_name = parse_choice(body.get('tool_choice'), functions)
    parallel = body.get('parallel_tool_calls', True)
    if not isinstance(parallel, bool):
        raise ValueError('parallel_tool_calls must be boolean')
    return ToolPolicy(functions, choice, forced_name, parallel)
