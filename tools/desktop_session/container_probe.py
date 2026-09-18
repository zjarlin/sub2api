"""Portable Linux probe. Read session JSON from stdin; output only a sanitized report."""
import argparse
import json
import sys
import uuid
from client import DesktopClient, validate_context
from transport import BOT_ID, TESTED_VERSION, request


def run(context, generate=False):
    validate_context(context)
    cookies, params = context['cookies'], context['params']
    report = {'desktop_version': TESTED_VERSION, 'runtime': sys.platform,
              'native_context_keys': sorted(params), 'generation_tested': False}
    with request(cookies, '/alice/user/get_account_info', params=params) as response:
        body = json.loads(response.read(1048576))
        report['authenticated'] = {'http_status': response.status, 'business_code': body.get('code')}
        if response.status != 200 or body.get('code') != 0:
            return report, 1
    if not generate:
        return report, 0
    client = DesktopClient(context)
    nonce = 'DESKTOP_CONTAINER_' + uuid.uuid4().hex[:10]
    reply = client.complete('不要调用工具，只回复原文：' + nonce,
                            client.selection['model']['model_item_key'])
    verified = reply.text.strip() == nonce
    report.update(generation_tested=True, generation_verified=verified,
                  desktop_conversation_isolated=True)
    return report, 0 if verified else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--generate', action='store_true')
    args = parser.parse_args()
    try:
        raw = sys.stdin.buffer.read(65537)
        if len(raw) > 65536:
            raise ValueError('Session input exceeded limit')
        report, code = run(json.loads(raw), args.generate)
    except Exception as error:
        report, code = {'outcome': 'probe_failed', 'error_type': type(error).__name__}, 1
    print(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(code)
