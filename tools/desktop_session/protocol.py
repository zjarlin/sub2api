"""Synthetic requests and conservative interpretation of the observed SSE protocol."""
import json
import time
import uuid
from transport import BOT_ID


def completion_request(nonce, selection):
    return chat_request('这是接口连通性测试。不要调用工具，只回复这段原文：' + nonce, selection)


def chat_request(text, selection, cursor=None):
    identifier = lambda: str(uuid.uuid4())
    model = selection['model']['model_item_key']
    mode = selection['mode_id']
    reasoning = selection['reasoning_effort']
    # 客户端枚举：对话为 Chat=2、conversation_mode=1，工作任务为 MOA=1、conversation_mode=2。
    modes = {'1': (1, 2), '3': (2, 1)}
    if mode not in modes:
        raise ValueError('Unsupported desktop conversation mode')
    conversation_mode, agent_mode = modes[mode]
    payload = {
        'client_meta': {'local_conversation_id': 'local_' + str(uuid.uuid4().int)[:16],
                        'conversation_id': '', 'bot_id': BOT_ID,
                        'last_section_id': '', 'last_message_index': None, 'local_permissions': []},
        'messages': [{'local_message_id': identifier(), 'message_status': 0,
                      'content_block': [{'block_type': 10000, 'block_id': identifier(),
                                         'parent_id': '', 'meta_info': [], 'append_fields': [],
                                         'content': {'text_block': {
                                             'text': text,
                                             'icon_url': '', 'icon_url_dark': '', 'summary': ''},
                                             'pc_event_block': ''}}]}],
        'option': {'create_time_ms': int(time.time() * 1000), 'unique_key': identifier(),
                   'need_create_conversation': True, 'conversation_mode': conversation_mode,
                   'conversation_init_option': {'need_ack_conversation': True},
                   'conversation_init_ext': {'model_item_key': model, 'mode_id': mode,
                                             'reasoning_effort': str(reasoning)},
                   'agent_mode': agent_mode, 'is_regen': False, 'start_seq': 0,
                   'model_config': {'model_item_key': model, 'model_extra_params': {},
                                    'reasoning_effort': reasoning},
                   'aggregate_params': {'conversation_mode': str(conversation_mode), 'mode_id': mode,
                                        'model_item_key': model, 'agent_mode': str(agent_mode),
                                        'reasoning_effort': str(reasoning), 'provider_id': ''},
                   'sse_recv_event_options': {'support_chunk_delta': False},
                   'support_lazy_fetch_stream': False},
        'user_context': [], 'ext': {'agent_mode': str(agent_mode), 'sub_conv_firstmet_type': '1',
                                  'conversation_init_option': '{"need_ack_conversation":true}'},
    }
    if cursor is not None:
        payload['client_meta'].update(local_conversation_id='', conversation_id=cursor.conversation_id,
                                      last_section_id=cursor.section_id,
                                      last_message_index=cursor.message_index)
        payload['option']['need_create_conversation'] = False
        payload['option'].pop('conversation_init_option')
        payload['option'].pop('conversation_init_ext')
        payload['ext'].pop('conversation_init_option')
        payload['ext'].pop('sub_conv_firstmet_type')
    return payload


def read_events(response, *, max_line_bytes=65536, max_total_bytes=1048576, max_seconds=600):
    event, data, size = '', [], 0
    started = time.monotonic()
    while True:
        line = response.readline(max_line_bytes + 1)
        size += len(line)
        if time.monotonic() - started > max_seconds:
            raise TimeoutError('Desktop generation deadline exceeded')
        if len(line) > max_line_bytes or size > max_total_bytes:
            raise ValueError('Stream exceeded the probe limit')
        if not line:
            return
        line = line.decode('utf-8').rstrip('\r\n')
        if not line:
            if data:
                yield event, json.loads('\n'.join(data))
            event, data = '', []
        elif line.startswith('event:'):
            event = line[6:].strip()
        elif line.startswith('data:'):
            data.append(line[5:].lstrip(' '))


def inspect_stream(response):
    result = {'http_status': response.status, 'event_types': [],
              'generation_verified': False, 'outcome': 'incomplete'}
    if response.status != 200 or 'text/event-stream' not in response.headers.get('Content-Type', ''):
        result['outcome'] = 'unexpected_http_response'
        return result
    for event, data in read_events(response, max_seconds=90):
        if event not in result['event_types']:
            result['event_types'].append(event)
        if event == 'STREAM_ERROR':
            result.update(outcome='upstream_error', business_code=data.get('error_code'))
            decision = data.get('extra', {}).get('decision', '{}')
            try:
                decision = json.loads(decision) if isinstance(decision, str) else decision
            except ValueError:
                decision = {}
            if isinstance(decision, dict) and decision.get('type') == 'verify':
                result.update(outcome='verification_required', verification_type=decision.get('subtype'))
            # No retry, no challenge payload logging, and no terminal event overriding the error.
            return result
        if event == 'SSE_REPLY_END' and data.get('end_type') == 3:
            # Normal output decoding still requires a successful live sample.
            result['outcome'] = 'completed_unverified'
            return result
    return result
