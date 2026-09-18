"""按已确认的历史续接桌面会话，保存摘要和结果以处理重试与进程重启。"""
import hashlib
import json
import os
import time
from collections import OrderedDict
from dataclasses import asdict, dataclass
from pathlib import Path
from models import ConversationCursor
from tool_calls import tool_prompt
from structured_output import format_prompt


def digest(value):
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return hashlib.sha256(raw.encode()).hexdigest()


def message_hashes(messages):
    normalized = []
    for message in messages:
        item = dict(message)
        if item.get('tool_calls'):
            item['content'] = item.get('content') or None
            item['tool_calls'] = [
                {**call, 'function': {**call['function'],
                                     'arguments': json.loads(call['function']['arguments'])}}
                for call in item['tool_calls']]
        normalized.append(digest(item))
    return normalized


def contract(request):
    return {'tools': asdict(request.tools), 'output_format': asdict(request.output_format)}


def continuation_text(request, offset, previous, previous_calls=()):
    text = ('以下 JSON 仅包含本次新增消息，请接着本会话已有上下文生成下一条助手回复。'
            '保留已有系统要求；若末条是 assistant，继续其未完成任务，不重复已有内容。'
            '不要执行豆包内置工具。\n' + json.dumps(request.messages[offset:], ensure_ascii=False))
    if previous_calls:
        text += '\n上一轮调用的客户端编号与函数对应关系：' + json.dumps(previous_calls, ensure_ascii=False)
    current = contract(request)
    previous_tools = previous['tools']
    if request.tools.enabled:
        changed = (previous_tools['choice'] == 'none' or
                   previous_tools['functions'] != current['tools']['functions'])
        # 每轮重申输出封装，防止“只回复正文”覆盖协议；未变化的函数结构不重复发送。
        return tool_prompt(text, request.tools, request.output_format, include_functions=changed)
    if previous_tools['choice'] != 'none' and previous_tools['functions']:
        text += '\n本轮禁止调用工具，直接输出最终答案，不再使用外层 content/tool_calls 对象。'
    if request.output_format.kind == 'text' and previous['output_format']['kind'] != 'text':
        text += '\n本轮最终答案为自然语言，不再要求之前的 JSON 格式。'
    return format_prompt(text, request.output_format)


@dataclass
class Plan:
    text: str
    cursor: object = None
    cached: object = None


class ConversationStore:
    def __init__(self, path=None, *, ttl=21600, max_entries=64, max_bytes=8 * 1024 * 1024, clock=time.time):
        self.path = Path(path) if path else None
        self.ttl, self.max_entries, self.max_bytes, self.clock = ttl, max_entries, max_bytes, clock
        self.entries, self.session = OrderedDict(), ''
        if self.path and self.path.exists():
            if self.path.stat().st_size > max_bytes:
                raise ValueError('Conversation state exceeded limit')
            saved = json.loads(self.path.read_text())
            if saved.get('version') != 1:
                raise ValueError('Unsupported conversation state version')
            self.session = saved['session']
            self.entries.update(saved['entries'])
        self.prune()

    def bind_session(self, session):
        identity = digest(session)
        if identity != self.session:
            self.entries.clear()
            self.session = identity
            self.persist()

    def prune(self):
        expired = [key for key, entry in self.entries.items() if self.clock() - entry['updated'] >= self.ttl]
        for key in expired:
            del self.entries[key]
        while len(self.entries) > self.max_entries:
            self.entries.popitem(last=False)

    def serialized(self):
        return json.dumps({'version': 1, 'session': self.session, 'entries': list(self.entries.items())},
                          ensure_ascii=False, separators=(',', ':')).encode()

    def persist(self):
        self.prune()
        raw = self.serialized()
        while len(raw) > self.max_bytes and self.entries:
            self.entries.popitem(last=False)
            raw = self.serialized()
        if self.path is None:
            return
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        temporary = self.path.with_suffix('.tmp')
        descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, 'wb') as output:
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, self.path)

    def plan(self, request, model, scope=''):
        self.prune()
        history = message_hashes(request.messages)
        scope_hash = digest(scope)
        current = contract(request)
        candidates = []
        for key, entry in reversed(self.entries.items()):
            if entry['model'] != model or entry['scope'] != scope_hash:
                continue
            # 无会话标识的首轮请求始终新建，避免将相同问句误当重试。
            retryable = bool(scope) or any(message['role'] == 'assistant' for message in request.messages)
            if retryable and history == entry['request'] and current == entry['contract']:
                candidates.append((key, entry, True))
                continue
            tip = entry['tip']
            if len(history) <= len(tip) or history[:len(tip)] != tip:
                continue
            candidates.append((key, entry, False))
        # 多个任务具有相同可见历史时，无法确定归属，重新初始化。
        if len(candidates) != 1:
            return Plan(request.text)
        key, entry, replay = candidates[0]
        if replay:
            return Plan('', cached=entry['result'])
        calls = entry['result']['choices'][0]['message'].get('tool_calls', [])
        text = continuation_text(request, len(entry['tip']), entry['contract'], calls)
        cursor = ConversationCursor(**entry['cursor'])
        # 发起请求前移除旧位置；上游失败或连接中断后不冒险重复追加。
        del self.entries[key]
        self.persist()
        return Plan(text, cursor)

    def commit(self, request, model, scope, reply, result, plan=None):
        cursor = getattr(reply, 'cursor', None)
        if not isinstance(cursor, ConversationCursor):
            return
        history = message_hashes(request.messages)
        assistant = result['choices'][0]['message']
        entry = {'model': model, 'scope': digest(scope), 'request': history,
                 'tip': history + message_hashes([assistant]), 'contract': contract(request),
                 'cursor': asdict(cursor), 'result': result, 'updated': self.clock()}
        if plan is not None:
            entry.update(input_bytes=len(plan.text.encode()), continued=plan.cursor is not None)
        self.entries[cursor.conversation_id] = entry
        self.entries.move_to_end(cursor.conversation_id)
        self.persist()
