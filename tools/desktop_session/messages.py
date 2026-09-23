from structured_output import json_object
from tool_policy import valid_name


def text_content(content, nullable=False):
    if isinstance(content, str) or (content is None and nullable):
        return content
    if not isinstance(content, list):
        raise ValueError('Only text message content is supported')
    parts = []
    for part in content:
        if (not isinstance(part, dict) or set(part) != {'type', 'text'} or
                part['type'] != 'text' or not isinstance(part['text'], str)):
            raise ValueError('Only text content parts are supported')
        parts.append(part['text'])
    return ''.join(parts)


def history_calls(calls, seen):
    if not isinstance(calls, list) or not 1 <= len(calls) <= 128:
        raise ValueError('assistant.tool_calls must contain 1 to 128 function calls')
    pending = set()
    for call in calls:
        if not isinstance(call, dict) or set(call) != {'id', 'type', 'function'} or call['type'] != 'function':
            raise ValueError('Invalid assistant function call')
        identifier, function = call['id'], call['function']
        if not isinstance(identifier, str) or not identifier.strip() or identifier in seen:
            raise ValueError('Tool call IDs must be non-empty and unique')
        if (not isinstance(function, dict) or set(function) != {'name', 'arguments'} or
                not valid_name(function['name']) or not isinstance(function['arguments'], str)):
            raise ValueError('Invalid assistant function name or arguments')
        json_object(function['arguments'], allow_fence=False)
        seen.add(identifier)
        pending.add(identifier)
    return pending


def normalize_message(message):
    if not isinstance(message, dict):
        raise ValueError('Each message must be an object')
    role = message.get('role')
    allowed = {'system': {'name'}, 'developer': {'name'}, 'user': {'name'},
               'assistant': {'name', 'tool_calls'}, 'tool': {'tool_call_id'}}
    if not isinstance(role, str) or role not in allowed or set(message) - ({'role', 'content'} | allowed[role]):
        raise ValueError('Unsupported message role or fields')
    if 'name' in message and not valid_name(message['name']):
        raise ValueError('Invalid message name')
    calls = message.get('tool_calls')
    nullable = role == 'assistant' and bool(calls)
    content = text_content(message.get('content'), nullable=nullable)
    result = {**message, 'content': content}
    if role == 'assistant' and (calls is None or calls == []):
        result.pop('tool_calls', None)
    return result


def parse_messages(messages):
    if not isinstance(messages, list) or not 1 <= len(messages) <= 100:
        raise ValueError('Expected 1 to 100 messages')
    normalized, pending, seen = [], set(), set()
    for original in messages:
        message = normalize_message(original)
        role = message['role']
        if pending and role != 'tool':
            raise ValueError('Each assistant tool call needs a result before the next message')
        if role == 'tool':
            identifier = message.get('tool_call_id')
            if not isinstance(identifier, str) or identifier not in pending:
                raise ValueError('Tool result has an unknown or already answered tool_call_id')
            pending.remove(identifier)
        if role == 'assistant' and message.get('tool_calls') is not None:
            pending = history_calls(message['tool_calls'], seen)
        normalized.append(message)
    if pending:
        raise ValueError('Missing tool results')
    return normalized
