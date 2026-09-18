"""Snapshot inspected desktop request context for a portable probe, without signatures."""
import re
import urllib.parse
from pathlib import Path
from session import read_cookies
from transport import PARAMS, check_version

CONTEXT_KEYS = {'device_id', 'tea_uuid', 'web_id', 'fp', 'msToken',
                'channel', 'chromium_version', 'client_platform', 'language',
                'region', 'sys_region', 'tz_name'}


def parse_native_params(log):
    matches = list(re.finditer(r'https://www\.doubao\.com/chat/completion\?[^\s\"\'<>]+', log))
    if not matches:
        raise RuntimeError('No recent native completion request context')
    query = dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(matches[-1].group()).query))
    if any(query.get(key) != value for key, value in PARAMS.items()):
        raise RuntimeError('Native request version or platform changed')
    if not query.get('device_id') or query.get('fp') != 'verify_' + query['device_id']:
        raise RuntimeError('Native device fingerprint did not match the inspected format')
    return {key: value for key, value in query.items() if key in CONTEXT_KEYS}


def snapshot():
    version = check_version()
    log_dir = Path.home() / 'Library/Application Support/Doubao/sdk_storage/log'
    candidates = list(log_dir.glob('saman_*.log'))
    if not candidates:
        raise RuntimeError('No desktop request log')
    latest = max(candidates, key=lambda path: path.stat().st_mtime)
    with latest.open('rb') as source:
        source.seek(max(0, latest.stat().st_size - 8 * 1024 * 1024))
        params = parse_native_params(source.read().decode(errors='replace'))
    cookies = read_cookies(include_browser_state=True)
    if cookies.get('msToken'):
        params['msToken'] = cookies['msToken']
    return {'desktop_version': version, 'params': params, 'cookies': cookies}
