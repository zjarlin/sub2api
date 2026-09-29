"""Responses history, previous-response chaining and isolated desktop cursors."""
from dataclasses import asdict, dataclass
from models import ConversationCursor
from responses import input_prompt, validate_history
from session_store import SessionStore, digest


def hashes(items):
    # Transport IDs and completion status are optional when replaying input.
    # Preserve semantic fields such as phase, namespace and call_id.
    return [digest({k: v for k, v in item.items() if k not in ('id', 'status')}) for item in items]


def contract(request):
    return {'instructions': request.instructions, 'tools': request.tools,
            'tool_declarations': request.tool_declarations,
            'output_format': asdict(request.output_format), 'store': request.store,
            'metadata': request.metadata, 'previous_response_id': request.previous_response_id}


@dataclass
class ResponsePlan:
    items: list
    text: str
    cursor: object = None
    cached: object = None


class ResponsesStore(SessionStore):
    def persist(self):
        import json
        self.prune()
        # Bound transient (store=false) entries as well as disk state.
        while self.entries and len(json.dumps(list(self.entries.items()), ensure_ascii=False).encode()) > self.max_bytes:
            self.entries.popitem(last=False)
        super().persist()

    def plan(self, request, model, scope=''):
        self.prune()
        items = request.items
        scope_hash = digest(scope)
        if request.previous_response_id:
            previous = self.entries.get(request.previous_response_id)
            if (not previous or previous['scope'] != scope_hash or previous['model'] != model or
                    previous.get('history') is None):
                raise ValueError('previous_response_id is unknown, expired, unstored or belongs to another session/model')
            items = previous['history'] + previous['result']['output'] + items
        validate_history(items)
        history, current = hashes(items), contract(request)
        candidates = []
        for key, entry in reversed(self.entries.items()):
            if entry['scope'] != scope_hash or entry['model'] != model:
                continue
            retryable = bool(request.previous_response_id) or any(
                item['type'] in ('function_call', 'custom_tool_call', 'tool_search_call') or item.get('role') == 'assistant' for item in items)
            if retryable and history == entry['request'] and current == entry['contract']:
                candidates.append((key, entry, True))
            elif entry.get('cursor') and len(history) > len(entry['tip']) and history[:len(entry['tip'])] == entry['tip']:
                candidates.append((key, entry, False))
        if len(candidates) != 1:
            return ResponsePlan(items, input_prompt(request, items))
        key, entry, replay = candidates[0]
        if replay:
            return ResponsePlan(items, '', cached=entry['result'])
        # A changed instruction cannot erase upstream context. Restart with full
        # history when it changes; this also supports branching from old responses.
        if current['instructions'] != entry['contract']['instructions']:
            return ResponsePlan(items, input_prompt(request, items))
        cursor = ConversationCursor(**entry['cursor'])
        text = input_prompt(request, items[len(entry['tip']):], continued=True,
                            include_tools=current['tools']['tools'] != entry['contract']['tools']['tools'])
        # Invalidate the position before sending; previous_response_id history
        # remains available for a fresh branch after an uncertain upstream failure.
        self.entries[key]['cursor'] = None
        self.persist()
        return ResponsePlan(items, text, cursor)

    def commit(self, request, model, scope, reply, result, plan):
        cursor = getattr(reply, 'cursor', None)
        if isinstance(cursor, ConversationCursor):
            for entry in self.entries.values():
                if (entry.get('cursor') or {}).get('conversation_id') == cursor.conversation_id:
                    entry['cursor'] = None
        entry = {'model': model, 'scope': digest(scope), 'request': hashes(plan.items),
                 'tip': hashes(plan.items + result['output']), 'contract': contract(request),
                 'cursor': asdict(cursor) if isinstance(cursor, ConversationCursor) else None,
                 'history': plan.items if request.store else None,
                 'result': result, 'updated': self.clock(), 'persistent': request.store}
        self.entries[result['id']] = entry
        self.persist()

    def serialized(self):
        import json
        # store=false may reuse a cursor in this process, but never persists a
        # request, response, instructions, or tool definitions on disk.
        return json.dumps({'version': 1, 'session': self.session,
                           'entries': [(k, v) for k, v in self.entries.items() if v.get('persistent')]},
                          ensure_ascii=False, separators=(',', ':')).encode()
