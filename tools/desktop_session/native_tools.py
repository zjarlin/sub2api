"""豆包 2.31.4 原生设备事件协议；不连接网络、不执行工具。"""
import copy
import json


class NativeProtocolError(ValueError):
    pass


def required_text(value):
    if not isinstance(value, str) or not value.strip():
        raise NativeProtocolError('Expected nonempty string')
    return value


def json_object(value):
    try:
        result = json.loads(value) if isinstance(value, str) else value
        json.dumps(result, allow_nan=False)
    except (ValueError, TypeError, RecursionError):
        raise NativeProtocolError('Invalid tool arguments')
    if not isinstance(result, dict):
        raise NativeProtocolError('Function arguments must be an object')
    return result


def connector_task_input(connector_id, functions):
    """按客户端 691459 模块编码工具目录，schema 不进入提示词。"""
    required_text(connector_id)
    tools, names = [], set()
    for function in functions:
        if not isinstance(function, dict):
            raise NativeProtocolError('Expected function definition object')
        name = required_text(function.get('name'))
        if name in names:
            raise NativeProtocolError('Duplicate tool name')
        description = function.get('description', '')
        if not isinstance(description, str):
            raise NativeProtocolError('Expected tool description string')
        parameters = json_object(function.get('parameters'))
        names.add(name)
        tools.append({'name': name, 'description': description,
                      'inputSchema': json.dumps(parameters, ensure_ascii=False, allow_nan=False)})
    if not tools:
        raise NativeProtocolError('At least one tool is required')
    return json.dumps({'schema_version': 1, 'localConnectors': [
        {'connectorId': connector_id, 'mcpToolsList': tools}]}, ensure_ascii=False)


class NativeToolExchange:
    """一个设备与 sandbox 的调用关联；实例不得跨账号或工作任务复用。"""

    def __init__(self, device_id, sandbox_id, allowed_names, connector_id=None):
        self.device_id = required_text(device_id)
        self.sandbox_id = required_text(sandbox_id)
        self.allowed_names = frozenset(required_text(name) for name in allowed_names)
        self.connector_id = required_text(connector_id) if connector_id is not None else None
        self.calls = {}
        self.results = {}

    def stream_payload(self):
        return {'device_id': self.device_id, 'client_attrs': {'sandbox_id': self.sandbox_id}}

    def receive(self, frame):
        if not isinstance(frame, dict):
            raise NativeProtocolError('Expected device event object')
        kind = frame.get('event_type')
        if type(kind) is not int or kind not in (1, 2, 3):
            raise NativeProtocolError('Unsupported device event type')
        sandbox = frame.get('sandbox_id', frame.get('instance_id', frame.get('sandBoxID')))
        if sandbox is not None and sandbox != self.sandbox_id:
            raise NativeProtocolError('Sandbox mismatch')
        if kind == 3:
            return None
        if kind == 2:
            raise NativeProtocolError('Device command requires a separate lifecycle handler')
        if sandbox != self.sandbox_id:
            raise NativeProtocolError('Tool call requires sandbox identity')
        call = frame.get('tool_call')
        if not isinstance(call, dict):
            raise NativeProtocolError('Missing tool call')
        identifier = required_text(call.get('id'))
        name, raw = self.function_input(call)
        # 签名、option、原始 input 全部保留；此层不声称完成原生验签。
        arguments = json_object(raw)
        encoded = json.dumps(arguments, ensure_ascii=False, allow_nan=False)
        signature = frame.get('signature')
        if signature is not None:
            if not isinstance(signature, dict) or signature.get('device_id') != self.device_id:
                raise NativeProtocolError('Signed device identity mismatch')
        previous = self.calls.get(identifier)
        if previous is not None:
            if previous['tool_call'] != frame['tool_call']:
                raise NativeProtocolError('Conflicting replay of tool call')
            return None
        self.calls[identifier] = copy.deepcopy(frame)
        return {'type': 'function_call', 'call_id': identifier, 'name': name,
                'arguments': raw if isinstance(raw, str) else encoded}

    def function_input(self, call):
        name = required_text(call.get('name'))
        raw = call.get('input')
        if self.connector_id is not None:
            if name != 'connector.call':
                raise NativeProtocolError('Only connector.call is supported for this task')
            envelope = json_object(raw)
            if type(envelope.get('protocol_version')) is not int or envelope['protocol_version'] != 1:
                raise NativeProtocolError('Unsupported connector protocol version')
            if envelope.get('connector_id') != self.connector_id:
                raise NativeProtocolError('Connector identity mismatch')
            name = required_text(envelope.get('remote_tool_name'))
            raw = envelope.get('raw_arguments')
            if not isinstance(raw, str):
                raise NativeProtocolError('Expected raw_arguments string')
        if name not in self.allowed_names:
            raise NativeProtocolError('Tool was not explicitly allowed')
        return name, raw

    def result_payload(self, call_id, output, is_error=False):
        if call_id not in self.calls:
            raise NativeProtocolError('Unknown call ID')
        if not isinstance(output, str) or type(is_error) is not bool:
            raise NativeProtocolError('Expected text output and boolean error flag')
        result = {'id': call_id, 'name': self.calls[call_id]['tool_call']['name'],
                  'content': [{'type': 'text', 'text': output}], 'isError': is_error}
        return self._store_result(call_id, result)

    def connector_result_payload(self, call_id, mcp_response_json):
        """透传本地 MCP 协议响应，不猜测或重写 JSON-RPC 的响应 ID。"""
        if self.connector_id is None or call_id not in self.calls:
            raise NativeProtocolError('Unknown connector call ID')
        if not isinstance(mcp_response_json, str):
            raise NativeProtocolError('Expected MCP response string')
        json_object(mcp_response_json)
        call = self.calls[call_id]['tool_call']
        original_input = call['input']
        if not isinstance(original_input, str):
            original_input = json.dumps(original_input, ensure_ascii=False, separators=(',', ':'))
        result = {'id': call_id, 'name': call['name'], 'raw_result': mcp_response_json,
                  'tool_input': original_input}
        return self._store_result(call_id, result)

    def _store_result(self, call_id, result):
        previous = self.results.get(call_id)
        if previous is not None and previous != result:
            raise NativeProtocolError('Conflicting tool result')
        self.results[call_id] = copy.deepcopy(result)
        # 仅构造载荷，是否上传成功由未来传输层确认，不在这里提交会话状态。
        return {'sandbox_id': self.sandbox_id, 'device_id': self.device_id, 'tool_result': result}
