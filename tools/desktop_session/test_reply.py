import copy
import io
import json
import unittest
from protocol import chat_request, completion_request
from models import ConversationCursor
from reply import CAPACITY_REPLIES, UpstreamError, decode_events, read_reply


class ReplyTests(unittest.TestCase):
    def setUp(self):
        self.payload = completion_request('synthetic', {
            'model': {'model_item_key': '9'}, 'mode_id': '3', 'reasoning_effort': 5})
        self.ack = {'ack_client_meta': {
            'conversation_id': '123', 'conversation_type': 3,
            'local_conversation_id': self.payload['client_meta']['local_conversation_id'],
            'conversation_info': {'extra': {'inner_app_id': '582478', 'model_item_key': '9',
                                            'mode_id': '3', 'reasoning_effort': '5'}}},
            'query_list': [{'message_index': 1}]}
        self.start = {'meta': {'user_type': 2, 'conversation_id': '123', 'message_id': '456'},
                      'content': {'content_block': []}}
        self.end = [('SSE_REPLY_END', {'end_type': 1, 'msg_finish_attr': {'msgid': '456'}}),
                    ('SSE_REPLY_END', {'end_type': 3})]

    def chunk(self, text, patch=1, **extra):
        return ('STREAM_CHUNK', {'message_id': '456', 'patch_op': [
            {'patch_object': 1, 'patch_value': {'content_block': [
                {'block_id': 'text', 'block_type': 10000, 'patch_type': patch,
                 'content': {'text_block': {'text': text}}, **extra}]}}]})

    def events(self, *chunks):
        return [('SSE_ACK', self.ack), ('STREAM_MSG_NOTIFY', self.start), *chunks, *self.end]

    def test_visible_text_append_and_replace_exclude_hidden_and_thinking(self):
        result = decode_events(self.events(self.chunk('draft'), self.chunk('Final', 2),
                                          self.chunk(' answer'), self.chunk('hidden', parent_id='thinking'),
                                          self.chunk('hidden', control_info={'is_visible': False})), self.payload)
        self.assertEqual(result.text, 'Final answer')

    def test_existing_conversation_and_wrong_model_are_rejected(self):
        for key, value in [('inner_app_id', '497858'), ('model_item_key', '5')]:
            events = copy.deepcopy(self.events(self.chunk('text')))
            events[0][1]['ack_client_meta']['conversation_info']['extra'][key] = value
            with self.assertRaisesRegex(UpstreamError, 'isolation_failed'):
                decode_events(events, self.payload)
        self.ack['query_list'][0]['message_index'] = 3298
        with self.assertRaisesRegex(UpstreamError, 'isolation_failed'):
            decode_events(self.events(self.chunk('text')), self.payload)

    def test_error_after_text_and_finish_cannot_be_success(self):
        events = self.events(self.chunk('text'))
        events.insert(-1, ('STREAM_ERROR', {'error_code': 710022004}))
        with self.assertRaisesRegex(UpstreamError, '710022004'):
            decode_events(events, self.payload)

    def test_chat_ack_cannot_silently_return_a_work_conversation(self):
        self.payload = chat_request('test', {'model': {'model_item_key': '3'},
                                            'mode_id': '1', 'reasoning_effort': 4})
        ack = self.ack['ack_client_meta']
        ack['local_conversation_id'] = self.payload['client_meta']['local_conversation_id']
        extra = ack['conversation_info']['extra']
        extra.update(model_item_key='3', mode_id='1', reasoning_effort='4')
        self.assertEqual(decode_events(self.events(self.chunk('answer')), self.payload).text, 'answer')
        extra['mode_id'] = '3'
        with self.assertRaisesRegex(UpstreamError, 'isolation_failed'):
            decode_events(self.events(self.chunk('answer')), self.payload)

    def test_continuation_rejects_an_explicit_mode_switch(self):
        self.continuation()
        self.ack['ack_client_meta']['conversation_info'] = {'extra': {'mode_id': '1'}}
        with self.assertRaisesRegex(UpstreamError, 'isolation_failed'):
            decode_events(self.events(self.chunk('answer')), self.payload)

    def continuation(self):
        self.payload = chat_request('next', {'model': {'model_item_key': '9'},
                                            'mode_id': '3', 'reasoning_effort': 5},
                                    ConversationCursor('123', '789', 2))
        self.ack = {'ack_client_meta': {'conversation_id': '123', 'conversation_type': 3,
                                        'section_id': '789'}, 'query_list': [{'message_index': 6}]}
        self.start['meta'].update(section_id='789', index_in_conv=7)

    def test_continuation_ack_without_initial_model_metadata(self):
        self.continuation()
        reply = decode_events(self.events(self.chunk('remembered')), self.payload)
        self.assertEqual(reply.cursor, ConversationCursor('123', '789', 7))

    def test_continuation_wrong_conversation_section_and_stale_index_rejected(self):
        for field, value in [('conversation_id', 'other'), ('section_id', 'other')]:
            self.continuation()
            self.ack['ack_client_meta'][field] = value
            with self.assertRaisesRegex(UpstreamError, 'isolation_failed'):
                decode_events(self.events(self.chunk('bad')), self.payload)
        self.continuation()
        self.ack['query_list'][0]['message_index'] = 2
        with self.assertRaisesRegex(UpstreamError, 'isolation_failed'):
            decode_events(self.events(self.chunk('bad')), self.payload)

    def test_continuation_wrong_assistant_section_or_index_rejected(self):
        for field, value in [('section_id', 'other'), ('index_in_conv', 2)]:
            self.continuation()
            self.start['meta'][field] = value
            with self.assertRaises(UpstreamError):
                decode_events(self.events(self.chunk('bad')), self.payload)

    def test_duplicate_ack_does_not_reset_validation(self):
        self.continuation()
        events = self.events(self.chunk('answer'))
        events.insert(1, events[0])
        with self.assertRaisesRegex(UpstreamError, 'duplicate_conversation_ack'):
            decode_events(events, self.payload)

    def test_input_echo_and_incomplete_output_are_not_assistant_reply(self):
        for events in [[('FULL_MSG_NOTIFY', {'message': {'user_type': 1}}), *self.end],
                       self.events(self.chunk('text'))[:-2]]:
            with self.assertRaises(UpstreamError):
                decode_events(events, self.payload)

    def test_unrequested_delta_cannot_silently_truncate_output(self):
        with self.assertRaisesRegex(UpstreamError, 'unsupported_chunk_delta'):
            decode_events(self.events(self.chunk('partial'), ('CHUNK_DELTA', {'text': 'rest'})), self.payload)

    def response(self, events):
        raw = ''.join('event: ' + name + '\ndata: ' + json.dumps(data) + '\n\n'
                      for name, data in events).encode()
        response = io.BytesIO(raw)
        response.status = 200
        response.headers = {'Content-Type': 'text/event-stream'}
        return response

    def test_large_escaped_context_echo_preserves_verified_assistant_reply(self):
        text = '上下文' * 70000
        self.payload['messages'][0]['content_block'][0]['content']['text_block']['text'] = text
        self.ack['query_list'][0]['content'] = text
        self.ack['query_list'][0]['content_block'] = [{'text': text}]
        events = self.events(self.chunk('Final answer'))
        events.insert(1, ('FULL_MSG_NOTIFY', {'message': {'user_type': 1, 'content': text}}))
        response = self.response(events)
        self.assertGreater(len(response.getvalue()), 1048576)
        self.assertEqual(read_reply(response, self.payload).text, 'Final answer')

    def test_unbounded_upstream_line_is_still_rejected_as_upstream_error(self):
        response = self.response(self.events(self.chunk('x' * 200000)))
        with self.assertRaisesRegex(UpstreamError, 'invalid_upstream_stream') as error:
            read_reply(response, self.payload)
        self.assertEqual(error.exception.status, 502)

    def test_verified_capacity_notice_is_not_a_successful_assistant_answer(self):
        for text in CAPACITY_REPLIES:
            with self.assertRaises(UpstreamError) as error:
                read_reply(self.response(self.events(self.chunk(text))), self.payload)
            self.assertEqual(error.exception.code, 'upstream_capacity')
            self.assertEqual(error.exception.status, 503)
            answer = '关于以下错误的说明：' + text
            self.assertEqual(read_reply(self.response(self.events(self.chunk(answer))), self.payload).text, answer)

    def test_three_context_representations_include_nested_json_escaping(self):
        text = '\\"' * 30000
        self.payload['messages'][0]['content_block'][0]['content']['text_block']['text'] = text
        echo = {'message': {'user_type': 1,
                           'content': json.dumps({'text': text}),
                           'content_block': self.payload['messages'][0]['content_block'],
                           'ext': {'raw_messages': json.dumps(self.payload['messages'])}}}
        events = self.events(self.chunk('Final answer'))
        events.insert(1, ('FULL_MSG_NOTIFY', echo))
        response = self.response(events)
        self.assertEqual(read_reply(response, self.payload).text, 'Final answer')
