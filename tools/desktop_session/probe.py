"""Inspect the desktop session; --generate sends exactly one synthetic work task."""
import argparse
import json
import uuid
from protocol import completion_request, inspect_stream
from session import read_cookies
from transport import check_version, read_catalog, read_json, request
from history import check_native_reply


def run(generate=False):
    version = check_version()
    cookies = read_cookies()
    anonymous_http, anonymous = read_json({}, '/alice/user/get_account_info')
    authenticated_http, authenticated = read_json(cookies, '/alice/user/get_account_info')
    report = {'desktop_version': version,
              'anonymous': {'http_status': anonymous_http, 'business_code': anonymous.get('code')},
              'authenticated': {'http_status': authenticated_http, 'business_code': authenticated.get('code')}}
    if authenticated_http != 200 or authenticated.get('code') != 0:
        report['outcome'] = 'session_unavailable'
        return report, 1
    models, defaults = read_catalog(cookies)
    report.update(models=models, default_office_selection=defaults.get('office'))
    if not generate:
        report['generation_tested'] = False
        return report, 0
    nonce = 'DESKTOP_API_' + uuid.uuid4().hex[:10]
    payload = completion_request(nonce, defaults['office'])
    with request(cookies, '/chat/completion', payload) as response:
        report['generation'] = inspect_stream(response)
    return report, 2 if report['generation']['outcome'] == 'verification_required' else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--generate', action='store_true', help='Send one synthetic desktop work task')
    mode.add_argument('--native-check', metavar='CONVERSATION_ID', help='Check a known native test conversation')
    parser.add_argument('--expect', help='Exact non-sensitive test marker expected from the assistant')
    args = parser.parse_args()
    if bool(args.native_check) != bool(args.expect):
        parser.error('--native-check and --expect must be used together')
    try:
        if args.native_check:
            version = check_version()
            result = check_native_reply(read_cookies(), args.native_check, args.expect)
            report = {'desktop_version': version, 'native_check': result}
            code = 0 if result['native_round_trip_verified'] else 1
        else:
            report, code = run(args.generate)
    except Exception as error:
        report, code = {'error_type': type(error).__name__, 'outcome': 'probe_failed'}, 1
    print(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(code)
