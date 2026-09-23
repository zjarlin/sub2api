import json
import re
from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError, ValidationError
from referencing import Registry, Resource
from referencing.exceptions import Unresolvable
from referencing.jsonschema import DRAFT202012
from models import OutputFormat


class OutputValidationError(ValueError):
    pass


def validate_references(resource, resolver):
    content = resource.contents
    if not isinstance(content, dict):
        return
    dialect = Draft202012Validator.META_SCHEMA['$id']
    if content.get('$schema', dialect) != dialect:
        raise ValueError('Only JSON Schema draft 2020-12 is supported')
    for key in ('$ref', '$dynamicRef'):
        reference = content.get(key)
        if reference is None:
            continue
        if not reference.startswith('#'):
            raise ValueError('Response schema references must be local fragments')
        resolver.lookup(reference)


def validate_schema(schema, label='response_format.json_schema.schema'):
    if not isinstance(schema, dict) or schema.get('type') != 'object':
        raise ValueError(label + ' must have type object')
    try:
        Draft202012Validator.check_schema(schema)
        root = Resource.from_contents(schema, default_specification=DRAFT202012)
        pending = [(root, Registry().resolver_with_root(root))]
        while pending:
            resource, resolver = pending.pop()
            validate_references(resource, resolver)
            pending.extend((child, resolver.in_subresource(child)) for child in resource.subresources())
    except (SchemaError, Unresolvable) as error:
        raise ValueError('Invalid ' + label + ' or local reference') from error


def parse_response_format(value):
    if value is None:
        return OutputFormat()
    if not isinstance(value, dict):
        raise ValueError('response_format must be an object')
    kind = value.get('type')
    if kind in ('text', 'json_object'):
        if set(value) != {'type'}:
            raise ValueError('Unexpected response_format fields')
        return OutputFormat(kind)
    if kind != 'json_schema' or set(value) != {'type', 'json_schema'}:
        raise ValueError('response_format.type must be text, json_object or json_schema')
    specification = value['json_schema']
    if not isinstance(specification, dict) or set(specification) - {'name', 'description', 'schema', 'strict'}:
        raise ValueError('Invalid response_format.json_schema fields')
    name = specification.get('name')
    if not isinstance(name, str) or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', name):
        raise ValueError('response_format.json_schema.name must contain 1 to 64 letters, digits, _ or -')
    if specification.get('strict') is not None and not isinstance(specification['strict'], bool):
        raise ValueError('response_format.json_schema.strict must be boolean')
    description = specification.get('description', '')
    if not isinstance(description, str):
        raise ValueError('response_format.json_schema.description must be text')
    schema = specification.get('schema')
    validate_schema(schema)
    return OutputFormat(kind, schema, description)


def format_prompt(text, output_format):
    if output_format.kind == 'text':
        return text
    instruction = '输出格式要求：只返回一个合法 JSON 对象，不要 Markdown 代码围栏或解释文字。'
    if output_format.schema is not None:
        specification = {'description': output_format.description, 'schema': output_format.schema}
        instruction += '\n返回对象必须符合以下 JSON Schema 定义：\n' + json.dumps(specification, ensure_ascii=False)
    return text + '\n\n' + instruction


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('Duplicate JSON object key')
        result[key] = value
    return result


def reject_constant(value):
    raise ValueError('Non-finite JSON number')


def json_object(text, allow_fence=True):
    candidate = text.strip()
    fenced = re.fullmatch(r'```(?:json)?\s*\n([\s\S]*?)\n```', candidate, re.IGNORECASE)
    if fenced and allow_fence:
        candidate = fenced.group(1).strip()
    value = json.loads(candidate, parse_constant=reject_constant, object_pairs_hook=unique_object)
    if not isinstance(value, dict):
        raise ValueError('Expected a JSON object')
    json.dumps(value, allow_nan=False)
    return value


def validate_output(text, output_format):
    if output_format is None or output_format.kind == 'text':
        return text
    try:
        value = json_object(text)
        if output_format.schema is not None:
            Draft202012Validator(output_format.schema, registry=Registry()).validate(value)
        return json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(',', ':'))
    except (ValueError, ValidationError, Unresolvable, RecursionError) as error:
        raise OutputValidationError('Desktop reply does not match the requested response_format') from error
