import unittest
from history import verify_reply


def conversation(identifier, text, user_type=2, content_status=0):
    return {'conversation': {'conversation_id': identifier, 'messages': [{
        'user_type': user_type, 'status': 0, 'content_status': content_status,
        'content_block': [{'block_type': 10000, 'content': {'text_block': {'text': text}}}],
    }]}}


class NativeReplyTests(unittest.TestCase):
    def test_completed_assistant_reply_matches(self):
        result = verify_reply([conversation('123', 'nonce')], '123', 'nonce')
        self.assertTrue(result['native_round_trip_verified'])

    def test_echoed_user_input_is_not_a_model_reply(self):
        result = verify_reply([conversation('123', 'nonce', user_type=1)], '123', 'nonce')
        self.assertFalse(result['native_round_trip_verified'])

    def test_other_conversations_streaming_text_and_partial_matches_are_rejected(self):
        cells = [conversation('999', 'nonce'), conversation('123', 'nonce', content_status=100),
                 conversation('123', 'nonce plus extra text')]
        result = verify_reply(cells, '123', 'nonce')
        self.assertFalse(result['native_round_trip_verified'])


if __name__ == '__main__':
    unittest.main()
