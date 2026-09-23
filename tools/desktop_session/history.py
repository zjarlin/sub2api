"""Verify a known native test conversation without exposing other conversations."""
import uuid
from transport import read_json


def verify_reply(cells, conversation_id, expected):
    result = {'conversation_found': False, 'assistant_reply_found': False,
              'native_round_trip_verified': False}
    for cell in cells:
        conversation = cell.get('conversation', {})
        if conversation.get('conversation_id') != conversation_id:
            continue
        result['conversation_found'] = True
        for message in conversation.get('messages', []):
            if message.get('user_type') != 2:
                continue
            result['assistant_reply_found'] = True
            if message.get('status') != 0 or message.get('content_status') != 0:
                continue
            text = ''.join(block.get('content', {}).get('text_block', {}).get('text', '')
                           for block in message.get('content_block', [])
                           if block.get('block_type') == 10000 and not block.get('parent_id'))
            if text.strip() == expected:
                result['native_round_trip_verified'] = True
                return result
    return result


def check_native_reply(cookies, conversation_id, expected):
    if not conversation_id.isdigit() or not expected:
        raise ValueError('A conversation ID and expected test marker are required')
    # This inspected endpoint returns a bounded recent window. Only the specified
    # test conversation contributes to the result; other metadata is discarded.
    payload = {
        'cmd': 3200, 'sequence_id': str(uuid.uuid4()), 'channel': 2, 'version': '1',
        'uplink_body': {'pull_recent_conv_chain_uplink_body': {
            'api_version': 1, 'conv_version': 0, 'direction': 3, 'limit': 5,
            'message_count_per_conv': 2,
            'option': {'not_need_message': False, 'need_complete_conversation': True,
                       'need_coco_conversation': False, 'need_coco_bot': False,
                       'need_pc_pin_chain': False, 'pc_pin_query_type': 0},
        }},
    }
    status, body = read_json(cookies, '/im/chain/recent_conv', payload)
    result = {'http_status': status, 'business_code': body.get('status_code'),
              'native_round_trip_verified': False}
    if status != 200 or body.get('status_code') != 0:
        return result
    cells = body.get('downlink_body', {}).get('pull_recent_conv_chain_downlink_body', {}).get('cells', [])
    return {**result, **verify_reply(cells, conversation_id, expected)}
