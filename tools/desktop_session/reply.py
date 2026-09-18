"""Decode verified, visible assistant text and enforce new desktop conversation isolation."""
import json
from dataclasses import dataclass, field
from protocol import read_events
from models import ConversationCursor

# 实测上游把资源不足作为完整助手正文返回，必须在格式校验和会话提交前识别。
CAPACITY_REPLIES = frozenset({
    '当前高峰期算力紧张，优先通道暂时繁忙。我们正在优先为你调度资源，请稍后再试。',
})


class UpstreamError(Exception):
    def __init__(self, code, status=502):
        self.code, self.status = str(code), status
        super().__init__(self.code)


@dataclass
class Reply:
    conversation_id: str = ''
    message_id: str = ''
    blocks: dict = field(default_factory=dict)
    finished: bool = False
    ended: bool = False
    section_id: str = ''
    message_index: int = 0

    @property
    def cursor(self):
        if self.conversation_id and self.section_id and self.message_index > 0:
            return ConversationCursor(self.conversation_id, self.section_id, self.message_index)
        return None

    @property
    def text(self):
        return ''.join(self.blocks.values())

    def apply_blocks(self, blocks):
        for block in blocks:
            if (block.get('block_type') != 10000 or block.get('parent_id') or
                    block.get('control_info', {}).get('is_visible') is False):
                continue
            text = block.get('content', {}).get('text_block', {}).get('text')
            if text is None:
                continue
            key = block['block_id']
            patch = block.get('patch_type', 2)
            if patch not in (1, 2) or not isinstance(text, str):
                raise UpstreamError('unsupported_text_patch')
            self.blocks[key] = (self.blocks.get(key, '') if patch == 1 else '') + text


def decode_events(events, payload):
    reply = Reply()
    for event, data in events:
        if event == 'STREAM_ERROR':
            code = data.get('error_code', 'upstream_error')
            raise UpstreamError(code, 503 if code == 710022004 else 502)
        if event == 'SSE_ACK':
            if reply.conversation_id:
                raise UpstreamError('duplicate_conversation_ack')
            ack = data.get('ack_client_meta', {})
            info = ack.get('conversation_info', {})
            extra = info.get('extra', '{}')
            extra = json.loads(extra) if isinstance(extra, str) else extra
            if not valid_ack(ack, extra, data.get('query_list', []), payload):
                raise UpstreamError('desktop_conversation_isolation_failed')
            reply.conversation_id = ack['conversation_id']
            reply.section_id = ack.get('section_id', '')
        elif event == 'STREAM_MSG_NOTIFY':
            meta = data.get('meta', {})
            if meta.get('user_type') != 2:
                continue
            if not reply.conversation_id or meta.get('conversation_id') != reply.conversation_id:
                raise UpstreamError('assistant_conversation_mismatch')
            if reply.message_id and meta.get('message_id') != reply.message_id:
                raise UpstreamError('multiple_assistant_messages_unsupported')
            reply.message_id = meta['message_id']
            if reply.section_id and meta.get('section_id') != reply.section_id:
                raise UpstreamError('assistant_section_mismatch')
            index = meta.get('index_in_conv', 0)
            previous = payload['client_meta'].get('last_message_index') or 0
            if not isinstance(index, int) or (previous and index <= previous):
                raise UpstreamError('assistant_message_index_mismatch')
            reply.message_index = index
            reply.apply_blocks(data.get('content', {}).get('content_block', []))
        elif event == 'STREAM_CHUNK':
            if not reply.message_id or data.get('message_id') != reply.message_id:
                raise UpstreamError('assistant_message_mismatch')
            for patch in data.get('patch_op', []):
                if patch.get('patch_object') == 1:
                    reply.apply_blocks(patch.get('patch_value', {}).get('content_block', []))
        elif event == 'CHUNK_DELTA':
            # Disabled in the request; fail visibly if the upstream ignores that option.
            raise UpstreamError('unsupported_chunk_delta')
        elif event == 'SSE_REPLY_END':
            if data.get('end_type') == 1:
                reply.finished = (bool(reply.message_id) and
                                  data.get('msg_finish_attr', {}).get('msgid') == reply.message_id)
            if data.get('end_type') == 3:
                reply.ended = True
                break
    if not (reply.conversation_id and reply.message_id and reply.text and reply.finished and reply.ended):
        raise UpstreamError('incomplete_assistant_reply')
    return reply


def valid_ack(ack, extra, queries, payload):
    meta = payload['client_meta']
    indices = [item.get('message_index') for item in queries]
    if not ack.get('conversation_id') or ack.get('conversation_type') != 3:
        return False
    if payload['option']['need_create_conversation']:
        selection = payload['option']['conversation_init_ext']
        return (ack.get('local_conversation_id') == meta['local_conversation_id'] and
                extra.get('inner_app_id') == '582478' and indices == [1] and
                all(extra.get(key) == value for key, value in selection.items()))
    # 续聊 ACK 不携带初始化配置，严格核对已有会话和递增的消息位置。
    expected_model = payload['option']['model_config']['model_item_key']
    return (ack['conversation_id'] == meta['conversation_id'] and
            ack.get('section_id') == meta['last_section_id'] and len(indices) == 1 and
            isinstance(indices[0], int) and indices[0] > meta['last_message_index'] and
            extra.get('model_item_key', expected_model) == expected_model and
            extra.get('inner_app_id', '582478') == '582478')


def read_reply(response, payload, *, max_seconds=600):
    if response.status != 200 or 'text/event-stream' not in response.headers.get('Content-Type', ''):
        raise UpstreamError('unexpected_upstream_http_status_' + str(response.status))
    request_json = json.dumps(payload)
    request_bytes = len(request_json.encode())
    nested_request_bytes = len(json.dumps(request_json).encode())
    max_line_bytes = request_bytes + 2 * nested_request_bytes + 65536
    max_total_bytes = 2 * max_line_bytes + 1048576
    try:
        events = read_events(response, max_line_bytes=max_line_bytes, max_total_bytes=max_total_bytes,
                             max_seconds=max_seconds)
        reply = decode_events(events, payload)
        if reply.text.strip() in CAPACITY_REPLIES:
            raise UpstreamError('upstream_capacity', 503)
        return reply
    except ValueError as error:
        raise UpstreamError('invalid_upstream_stream') from error
