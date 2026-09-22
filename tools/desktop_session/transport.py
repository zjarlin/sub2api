"""Version-specific desktop protocol transport; no API keys or cookie files."""
import json
import plistlib
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

APP = Path('/Applications/doubao.app/Contents/Info.plist')
TESTED_VERSION = '2.29.10'
BOT_ID = '7338286299411103781'
PARAMS = {
    'aid': '582478', 'real_aid': '582478', 'device_platform': 'web',
    'doubao_device_platform': 'desktop', 'pc_version': TESTED_VERSION,
    'doubao_pc_version': TESTED_VERSION, 'version_code': '20800',
    'runtime': 'web', 'runtime_version': '3.37.0', 'pkg_type': 'release_version',
    'web_platform': 'desktop', 'samantha_web': '1', 'use-olympus-account': '1',
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def check_version():
    version = plistlib.loads(APP.read_bytes())['CFBundleShortVersionString']
    if version != TESTED_VERSION:
        raise RuntimeError('Desktop version changed; recheck the protocol before generating')
    return version


def request(cookies, path, payload=None, *, params=None, timeout=20):
    # Only the inspected vendor origin is permitted. Redirects cannot forward cookies.
    if not path.startswith('/') or path.startswith('//') or '?' in path or '#' in path:
        raise ValueError('Expected a relative API path')
    query = dict(PARAMS)
    if params:
        query.update(params)
    url = 'https://www.doubao.com' + path + '?' + urllib.parse.urlencode(query)
    headers = {'Content-Type': 'application/json', 'Agw-Js-Conv': 'str',
               'Origin': 'https://www.doubao.com', 'Referer': 'https://www.doubao.com/'}
    if path.startswith('/im/'):
        headers['Content-Type'] = 'application/json; encoding=utf-8'
    if cookies:
        headers['Cookie'] = '; '.join(name + '=' + value for name, value in cookies.items())
    req = urllib.request.Request(url, data=json.dumps(payload or {}).encode(),
                                 headers=headers, method='POST')
    try:
        return urllib.request.build_opener(NoRedirect).open(req, timeout=timeout)
    except urllib.error.HTTPError as error:
        return error


def read_json(cookies, path, payload=None, *, params=None):
    with request(cookies, path, payload, params=params) as response:
        raw = response.read(1048577)
        if len(raw) > 1048576:
            raise ValueError('Response exceeded the probe limit')
        return response.status, json.loads(raw)


def read_catalog(cookies, *, params=None):
    status, body = read_json(cookies, '/alice/slot/action_bar_v3/brief_list',
                             {'bot_id': BOT_ID, 'language_code': 'zh'}, params=params)
    if status != 200 or body.get('code') != 0:
        raise RuntimeError('Desktop model catalog request failed')
    for entry in body.get('data', {}).get('entry_list', []):
        menu = entry.get('active_switch_conf', {}).get('menu_conf_v2', {})
        items = menu.get('model_list', {}).get('item_list', [])
        if items:
            models = [{key: item.get(key) for key in ('model_item_key', 'name', 'default_mode',
                                                     'reasoning_effort_config')}
                      for item in items]
            return models, menu.get('default_select_mode_map', {})
    raise RuntimeError('No desktop models returned')
