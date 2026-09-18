"""Desktop subscription client with dynamic menu selection and no desktop tool executor."""
from protocol import chat_request
import http.client
import math
import os
import urllib.error
from reply import UpstreamError, read_reply
from transport import PARAMS, TESTED_VERSION, read_catalog, request

MODEL_KEYS = {'doubao-auto': '9', 'doubao-pro': '5'}


def timeout_setting(name, default):
    value = float(os.environ.get(name, default))
    if not math.isfinite(value) or value <= 0 or value > 1800:
        raise ValueError(name + ' must be between 0 and 1800 seconds (exclusive of zero)')
    return value


def validate_context(context):
    if (context.get('desktop_version') != TESTED_VERSION or
            not isinstance(context.get('cookies'), dict) or
            not context['cookies'].get('sessionid') or
            not isinstance(context.get('params'), dict)):
        raise ValueError('Invalid desktop session snapshot')
    if set(context['params']) & (set(PARAMS) | {'a_bogus'}):
        raise ValueError('Session must not override application identity or replay signatures')
    return context


class DesktopClient:
    def __init__(self, context):
        self.read_timeout = timeout_setting('DESKTOP_UPSTREAM_TIMEOUT_SECONDS', 120)
        self.generation_timeout = timeout_setting('DESKTOP_GENERATION_TIMEOUT_SECONDS', 600)
        self.context = validate_context(context)
        self.cookies, self.params = context['cookies'], context['params']
        models, defaults = read_catalog(self.cookies, params=self.params)
        self.selection = defaults['office']
        work_models = {m['model_item_key']: m for m in models if str(m['default_mode']) == '3'}
        self.models = {name: {**work_models[key], 'id': name}
                       for name, key in MODEL_KEYS.items() if key in work_models}

    def resolve_model(self, model):
        item = self.models.get(model) or next(
            (item for item in self.models.values() if item['model_item_key'] == model), None)
        if item is None:
            raise ValueError('Unknown desktop work model; use /v1/models')
        return item

    def complete(self, text, model, cursor=None):
        item = self.resolve_model(model)
        selection = {**self.selection, 'model': {'model_item_key': item['model_item_key']}}
        payload = chat_request(text, selection, cursor)
        try:
            with request(self.cookies, '/chat/completion', payload, params=self.params,
                         timeout=self.read_timeout) as response:
                return read_reply(response, payload, max_seconds=self.generation_timeout)
        except TimeoutError as error:
            raise UpstreamError('upstream_timeout', 504) from error
        except urllib.error.URLError as error:
            if isinstance(error.reason, TimeoutError):
                raise UpstreamError('upstream_timeout', 504) from error
            raise UpstreamError('upstream_connection_failed') from error
        except (OSError, http.client.HTTPException) as error:
            raise UpstreamError('upstream_connection_failed') from error
