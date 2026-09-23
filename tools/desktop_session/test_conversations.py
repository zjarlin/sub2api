import copy
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock
from api import completion, parse_request
from conversations import ConversationStore
from reply import Reply, UpstreamError
from server import Adapter
from test_tool_calls import function_tool


class ConversationTests(unittest.TestCase):
    def setUp(self):
        self.now = 100
        self.store = ConversationStore(clock=lambda: self.now, ttl=60)
        self.body = {'model': 'doubao-pro', 'messages': [
            {'role': 'system', 'content': 'PRIVATE_SYSTEM_' + 'x' * 70000},
            {'role': 'user', 'content': '记住标记 alpha'}]}
        self.first = parse_request(self.body)
        self.reply = Reply(conversation_id='123', message_id='456', section_id='789', message_index=2,
                           blocks={'text': '已记住'}, finished=True, ended=True)
        self.result = completion('doubao-pro', self.reply)

    def commit(self, scope=''):
        self.store.commit(self.first, 'doubao-pro', scope, self.reply, self.result)

    def next_request(self, extra=None):
        body = copy.deepcopy(self.body)
        body['messages'].extend([self.result['choices'][0]['message'],
                                 extra or {'role': 'user', 'content': '回复标记'}])
        return parse_request(body)

    def test_continuation_sends_only_new_messages_and_consumes_old_cursor(self):
        self.commit()
        request = self.next_request()
        plan = self.store.plan(request, 'doubao-pro')
        self.assertEqual(plan.cursor, self.reply.cursor)
        self.assertNotIn('PRIVATE_SYSTEM_', plan.text)
        self.assertNotIn('记住标记 alpha', plan.text)
        self.assertIn('回复标记', plan.text)
        self.assertLess(len(plan.text), 300)
        self.assertIsNone(self.store.plan(request, 'doubao-pro').cursor)

    def test_other_scopes_models_and_edited_history_do_not_reuse(self):
        self.commit('task-a')
        self.assertIsNone(self.store.plan(self.next_request(), 'doubao-pro', 'task-b').cursor)
        self.assertIsNone(self.store.plan(self.next_request(), 'doubao-auto', 'task-a').cursor)
        request = self.next_request()
        request.messages[0]['content'] = 'Changed system'
        self.assertIsNone(self.store.plan(request, 'doubao-pro', 'task-a').cursor)
        self.assertIsNotNone(self.store.plan(self.next_request(), 'doubao-pro', 'task-a').cursor)

    def test_branches_do_not_append_to_future_context(self):
        self.commit()
        second = self.next_request()
        self.store.plan(second, 'doubao-pro')
        self.reply.message_index = 7
        self.store.commit(second, 'doubao-pro', '', self.reply, self.result)
        branch = self.next_request({'role': 'user', 'content': '另一个分支'})
        self.assertIsNone(self.store.plan(branch, 'doubao-pro').cursor)

    def test_identical_first_turn_is_new_without_explicit_session(self):
        self.commit()
        self.assertIsNone(self.store.plan(self.first, 'doubao-pro').cached)
        self.commit('explicit')
        self.assertEqual(self.store.plan(self.first, 'doubao-pro', 'explicit').cached, self.result)

    def test_identical_independent_histories_are_not_arbitrarily_merged(self):
        self.commit()
        self.reply.conversation_id = 'another'
        self.commit()
        self.assertIsNone(self.store.plan(self.next_request(), 'doubao-pro').cursor)
        self.assertEqual(len(self.store.entries), 2)

    def test_retry_replays_identical_result_without_advancing(self):
        self.commit()
        second = self.next_request()
        self.store.plan(second, 'doubao-pro')
        self.store.commit(second, 'doubao-pro', '', self.reply, self.result)
        plan = self.store.plan(second, 'doubao-pro')
        self.assertEqual(plan.cached['id'], self.result['id'])
        self.assertIsNone(plan.cursor)

    def test_persistence_expiry_and_session_refresh(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'state.json'
            self.store = ConversationStore(path, clock=lambda: self.now, ttl=60)
            self.store.bind_session({'cookies': {'sessionid': 'test-session'}})
            self.commit()
            self.assertNotIn('PRIVATE_SYSTEM_', path.read_text())
            self.assertNotIn('test-session', path.read_text())
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            restored = ConversationStore(path, clock=lambda: self.now, ttl=60)
            restored.bind_session({'cookies': {'sessionid': 'test-session'}})
            self.assertIsNotNone(restored.plan(self.next_request(), 'doubao-pro').cursor)
            self.store.bind_session({'cookies': {'sessionid': 'other-session'}})
            self.assertIsNone(self.store.plan(self.next_request(), 'doubao-pro').cursor)
            self.commit()
            self.now += 61
            self.assertIsNone(self.store.plan(self.next_request(), 'doubao-pro').cursor)

    def test_entry_and_byte_limits_are_enforced(self):
        self.store.max_entries = 2
        for index in range(3):
            self.reply.conversation_id = str(index)
            self.commit()
        self.assertEqual(list(self.store.entries), ['1', '2'])
        self.store.max_bytes = 100
        self.store.persist()
        self.assertLessEqual(len(self.store.serialized()), 100)

    def test_tools_keep_result_id_mapping_without_resending_schema(self):
        self.body.update(tools=[function_tool()], tool_choice='required')
        self.first = parse_request(self.body)
        self.reply.blocks = {'text': json.dumps({'content': None, 'tool_calls': [
            {'name': 'lookup', 'arguments': {'query': 'alpha'}}]})}
        self.result = completion('doubao-pro', self.reply, self.first.output_format, self.first.tools)
        self.commit()
        call = self.result['choices'][0]['message']['tool_calls'][0]
        self.body['tool_choice'] = 'auto'
        request = self.next_request({'role': 'tool', 'tool_call_id': call['id'], 'content': 'RANDOM_RESULT'})
        plan = self.store.plan(request, 'doubao-pro')
        self.assertIsNotNone(plan.cursor)
        self.assertIn(call['id'], plan.text)
        self.assertIn('lookup', plan.text)
        self.assertIn('RANDOM_RESULT', plan.text)
        self.assertIn('auto', plan.text)
        self.assertNotIn('"parameters":', plan.text)
        self.assertNotIn('PRIVATE_SYSTEM_', plan.text)

    def test_changed_schema_is_transmitted_and_tool_disable_is_explicit(self):
        self.commit()
        self.body.update(tools=[function_tool()])
        request = self.next_request()
        self.assertIn('"parameters":', self.store.plan(request, 'doubao-pro').text)
        self.first = request
        self.commit()
        body = copy.deepcopy(self.body)
        body['messages'] = request.messages + [self.result['choices'][0]['message'],
                                                {'role': 'user', 'content': '继续'}]
        body['tool_choice'] = 'none'
        plan = self.store.plan(parse_request(body), 'doubao-pro')
        self.assertIsNotNone(plan.cursor)
        self.assertIn('本轮禁止调用工具', plan.text)

    def test_invalid_reply_or_transport_failure_never_commits_cursor(self):
        adapter = object.__new__(Adapter)
        adapter.capacity_until = {}
        adapter.conversations = self.store
        adapter.client = Mock()
        adapter.client.resolve_model.return_value = {'id': 'doubao-pro'}
        self.commit()
        adapter.client.complete.side_effect = UpstreamError('interrupted')
        with self.assertRaises(UpstreamError):
            adapter.complete(self.next_request())
        self.assertEqual(len(self.store.entries), 0)
        self.assertIsNone(self.store.plan(self.next_request(), 'doubao-pro').cursor)
