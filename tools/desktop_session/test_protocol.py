import io
import json
import unittest
from unittest.mock import patch
from protocol import chat_request, completion_request, inspect_stream, read_events
from models import ConversationCursor
from transport import NoRedirect, request


class Response(io.BytesIO):
    status = 200
    headers = {'Content-Type': 'text/event-stream'}


def stream(*events):
    return Response(''.join('event: ' + name + '\r\ndata: ' + json.dumps(data) + '\r\n\r\n'
                            for name, data in events).encode())


class ProtocolTests(unittest.TestCase):
    def test_http_200_and_terminal_event_do_not_hide_verification_failure(self):
        response = stream(('SSE_HEARTBEAT', {}), ('STREAM_ERROR', {
            'error_code': 710022004, 'error_msg': 'rate limited',
            'extra': {'decision': json.dumps({'type': 'verify', 'subtype': 'slide',
                                            'detail': 'SENSITIVE_CHALLENGE'})},
        }), ('SSE_REPLY_END', {'end_type': 3}))
        result = inspect_stream(response)
        self.assertEqual(result['outcome'], 'verification_required')
        self.assertEqual(result['business_code'], 710022004)
        self.assertFalse(result['generation_verified'])
        self.assertNotIn('SENSITIVE_CHALLENGE', json.dumps(result))

    def test_early_eof_is_not_success(self):
        self.assertEqual(inspect_stream(stream(('SSE_HEARTBEAT', {})))['outcome'], 'incomplete')

    def test_a_finish_signal_alone_cannot_prove_generated_output(self):
        result = inspect_stream(stream(('SSE_REPLY_END', {'end_type': 3})))
        self.assertEqual(result['outcome'], 'completed_unverified')
        self.assertFalse(result['generation_verified'])

    def test_two_probes_request_unique_local_conversations(self):
        selection = {'model': {'model_item_key': '9'}, 'mode_id': '3', 'reasoning_effort': 5}
        first = completion_request('nonce-a', selection)
        second = completion_request('nonce-b', selection)
        self.assertNotEqual(first['client_meta']['local_conversation_id'],
                            second['client_meta']['local_conversation_id'])
        self.assertNotEqual(first['option']['unique_key'], second['option']['unique_key'])
        self.assertEqual(first['client_meta']['conversation_id'], '')
        self.assertEqual(first['client_meta']['local_permissions'], [])

    def test_oversized_stream_is_rejected(self):
        with self.assertRaises(ValueError):
            inspect_stream(Response(b'data: ' + b'x' * 65536))

    def test_chat_mode_is_consistent_on_first_turn_and_continuation(self):
        selection = {'model': {'model_item_key': '3'}, 'mode_id': '1', 'reasoning_effort': 4}
        for cursor in (None, ConversationCursor('123', '456', 7)):
            payload = chat_request('next', selection, cursor)
            self.assertEqual(payload['option']['agent_mode'], 2)
            self.assertEqual(payload['option']['conversation_mode'], 1)
            self.assertEqual(payload['option']['aggregate_params']['agent_mode'], '2')
            self.assertEqual(payload['option']['aggregate_params']['mode_id'], '1')
            self.assertEqual(payload['ext']['agent_mode'], '2')
            self.assertNotIn('general_task_param', payload['ext'])
            self.assertNotIn('client_tool_key', payload['ext'])

    def test_unknown_modes_are_rejected_before_sending(self):
        with self.assertRaises(ValueError):
            chat_request('test', {'model': {'model_item_key': '5'}, 'mode_id': '2', 'reasoning_effort': 5})

    def test_continuation_uses_cursor_and_omits_creation_fields(self):
        payload = chat_request('next message',
                               {'model': {'model_item_key': '5'}, 'mode_id': '3', 'reasoning_effort': 5},
                               ConversationCursor('123', '456', 7))
        self.assertEqual(payload['client_meta']['conversation_id'], '123')
        self.assertEqual(payload['client_meta']['last_section_id'], '456')
        self.assertEqual(payload['client_meta']['last_message_index'], 7)
        self.assertEqual(payload['client_meta']['local_conversation_id'], '')
        self.assertFalse(payload['option']['need_create_conversation'])
        self.assertNotIn('conversation_init_ext', payload['option'])
        self.assertNotIn('sub_conv_firstmet_type', payload['ext'])

    def test_configured_total_stream_limit_is_enforced(self):
        response = stream(*[('SSE_HEARTBEAT', {})] * 100)
        with self.assertRaises(ValueError):
            list(read_events(response, max_line_bytes=64, max_total_bytes=128))

    def test_long_generation_survives_old_deadline_but_remains_bounded(self):
        for elapsed, succeeds in ((91, True), (601, False)):
            response = stream(('SSE_HEARTBEAT', {}))
            with patch('protocol.time.monotonic', side_effect=[0, elapsed, elapsed, elapsed, elapsed]):
                if succeeds:
                    self.assertEqual(list(read_events(response)), [('SSE_HEARTBEAT', {})])
                else:
                    with self.assertRaises(TimeoutError):
                        list(read_events(response))

    def test_cookie_transport_rejects_external_destinations(self):
        for path in ('https://example.com', '//example.com', '/path?token=secret'):
            with self.assertRaises(ValueError):
                request({'sessionid': 'secret'}, path)
        self.assertIsNone(NoRedirect().redirect_request(None, None, 302, None, {}, 'https://example.com'))


if __name__ == '__main__':
    unittest.main()
