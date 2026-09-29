"""Validate custom-tool text after generation; no constrained decoding claim."""
import re
from functools import lru_cache
from lark import Lark
from lark.exceptions import LarkError


@lru_cache(maxsize=16)
def compile_grammar(syntax, definition):
    if not isinstance(definition, str) or not definition.strip():
        raise ValueError('Custom grammar definition must be non-empty text')
    try:
        if syntax == 'regex':
            return re.compile(definition)
        if syntax != 'lark':
            raise ValueError('Custom grammar syntax must be lark or regex')
        # User grammars may import Lark's bundled common terminals, never files
        # from the adapter filesystem or another Python package.
        for module in re.findall(r'%import\s+([^\s(]+)', definition):
            if module != 'common' and not module.startswith('common.'):
                raise ValueError('Only bundled common Lark imports are supported')
        return Lark(definition, parser='earley')
    except (LarkError, re.error) as error:
        raise ValueError('Invalid custom tool grammar') from error


def validate_format(fmt):
    if not isinstance(fmt, dict):
        raise ValueError('Custom tool format must be an object')
    if fmt == {'type': 'text'}:
        return
    if fmt.get('type') != 'grammar' or set(fmt) != {'type', 'syntax', 'definition'}:
        raise ValueError('Invalid custom tool format')
    if not isinstance(fmt['syntax'], str) or not isinstance(fmt['definition'], str):
        raise ValueError('Custom grammar syntax and definition must be text')
    compile_grammar(fmt['syntax'], fmt['definition'])


def validate_input(value, fmt):
    if not fmt or fmt['type'] == 'text':
        return
    parser = compile_grammar(fmt['syntax'], fmt['definition'])
    try:
        if fmt['syntax'] == 'regex':
            if parser.fullmatch(value) is None:
                raise ValueError('Custom input does not match its grammar')
        else:
            parser.parse(value)
    except LarkError as error:
        raise ValueError('Custom input does not match its grammar') from error
