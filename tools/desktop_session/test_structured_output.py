import json
import unittest
from structured_output import OutputValidationError, format_prompt, parse_response_format, validate_output


SCHEMA = {'type': 'object', 'properties': {'answer': {'type': 'string'}, 'count': {'type': 'integer'}},
          'required': ['answer', 'count'], 'additionalProperties': False}


def schema_format(schema=SCHEMA, **options):
    return {'type': 'json_schema', 'json_schema': {'name': 'result', 'schema': schema, **options}}


class StructuredOutputTests(unittest.TestCase):
    def test_missing_null_and_text_preserve_prompt_and_response(self):
        for value in (None, {'type': 'text'}):
            output_format = parse_response_format(value)
            self.assertEqual(format_prompt('original prompt', output_format), 'original prompt')
            self.assertEqual(validate_output('plain text', output_format), 'plain text')

    def test_json_object_instruction_and_whole_fence_normalization(self):
        output_format = parse_response_format({'type': 'json_object'})
        self.assertIn('JSON', format_prompt('original prompt', output_format))
        for text in ('{"answer":"好"}', '```json\n{"answer":"好"}\n```'):
            self.assertEqual(json.loads(validate_output(text, output_format)), {'answer': '好'})

    def test_json_object_rejects_invalid_or_ambiguous_output(self):
        output_format = parse_response_format({'type': 'json_object'})
        for text in ('[]', 'null', 'true', '1', 'prefix {"answer":1}', '{"answer":1} trailing',
                     '{"answer":', '{"answer":NaN}', '{"answer":Infinity}', '{"answer":1e400}',
                     '{"answer":1,"answer":2}'):
            with self.subTest(text=text), self.assertRaises(OutputValidationError):
                validate_output(text, output_format)

    def test_schema_is_in_prompt_and_enforced_for_both_strict_values(self):
        for strict in (True, False):
            output_format = parse_response_format(schema_format(strict=strict, description='回答与计数'))
            prompt = format_prompt('original prompt', output_format)
            self.assertIn('additionalProperties', prompt)
            self.assertIn('回答与计数', prompt)
            self.assertEqual(json.loads(validate_output('{"answer":"ok","count":2}', output_format)),
                             {'answer': 'ok', 'count': 2})
            for text in ('{}', '{"answer":"ok","count":"2"}', '{"answer":"ok","count":true}',
                         '{"answer":"ok","count":2,"extra":1}'):
                with self.subTest(strict=strict, text=text), self.assertRaises(OutputValidationError):
                    validate_output(text, output_format)

    def test_local_refs_nested_constraints_and_property_named_ref(self):
        schema = {'type': 'object', '$defs': {'answer': {'type': 'string', 'enum': ['ok']}},
                  'properties': {'$ref': {'$ref': '#/$defs/answer'},
                                 'items': {'type': 'array', 'items': {'type': 'integer', 'minimum': 1}}},
                  'required': ['$ref', 'items'], 'additionalProperties': False}
        output_format = parse_response_format(schema_format(schema))
        self.assertEqual(json.loads(validate_output('{"$ref":"ok","items":[1]}', output_format)),
                         {'$ref': 'ok', 'items': [1]})
        with self.assertRaises(OutputValidationError):
            validate_output('{"$ref":"ok","items":[0]}', output_format)

    def test_invalid_formats_fail_before_generation(self):
        invalid = ('json_object', [], {}, {'type': 'yaml'}, {'type': 'text', 'schema': {}},
                   {'type': 'json_schema'}, {'type': 'json_schema', 'json_schema': []},
                   schema_format(name='bad name'), schema_format(strict='true'),
                   schema_format(description=123), schema_format(schema=[]),
                   schema_format(schema={'type': 'object', 'required': 'bad'}),
                   schema_format(schema={'type': 'object', '$ref': 'https://example.com/schema'}),
                   schema_format(schema={'type': 'object', '$ref': '#/$defs/missing'}),
                   schema_format(schema={'type': 'object', '$schema': 'http://json-schema.org/draft-07/schema#'}))
        for value in invalid:
            with self.subTest(value=value), self.assertRaises(ValueError):
                parse_response_format(value)
