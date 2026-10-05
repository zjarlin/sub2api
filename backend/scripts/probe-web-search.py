#!/usr/bin/env python3
"""统一探查分组搜索能力，逐条结果由网关保存，凭据从环境变量读取。"""
import argparse
import concurrent.futures
import json
import os
import pathlib
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', required=True)
    parser.add_argument('--group-id', type=int, required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--workers', type=int, choices=[1, 2], default=2)
    args = parser.parse_args()
    key = os.environ.get('SUB2API_ADMIN_KEY', '')
    if not key or args.group_id <= 0:
        parser.error('SUB2API_ADMIN_KEY and a positive group-id are required')
    root = args.base_url.rstrip('/') + '/api/v1/admin/settings/search-fallback'

    def request(path='', payload=None):
        body = None if payload is None else json.dumps(payload).encode()
        req = urllib.request.Request(root + path, data=body, headers={
            'x-api-key': key, 'Content-Type': 'application/json', 'User-Agent': 'Mozilla/5.0',
        }, method='PUT' if payload is not None and path == '' else ('POST' if payload is not None else 'GET'))
        with urllib.request.urlopen(req, timeout=70) as response:
            return json.load(response)['data']

    candidates = request('/candidates?group_id=' + str(args.group_id))
    print('Discovered {} native-search account/model targets'.format(len(candidates)), flush=True)
    report = {'group_id': args.group_id, 'started_at': time.time(), 'results': [], 'errors': []}

    def probe(candidate):
        try:
            result = request('/probe', {'group_id': args.group_id, 'account_id': candidate['account_id'], 'model': candidate['model']})
            return result, None
        except Exception as error:
            # 不保存响应正文、请求头或凭据。
            status = getattr(error, 'code', None)
            return None, {'account_id': candidate['account_id'], 'model': candidate['model'], 'error': 'HTTP {}'.format(status) if status else type(error).__name__}

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = [pool.submit(probe, candidate) for candidate in candidates]
        for completed, future in enumerate(concurrent.futures.as_completed(futures), 1):
            result, error = future.result()
            if result:
                report['results'].append(result)
                print('{}/{} account={} model={} status={} actual={}'.format(completed, len(candidates), result['account_id'], result['model'], result['status'], result.get('actual_model', '')), flush=True)
            else:
                report['errors'].append(error)
                print('{}/{} {}'.format(completed, len(candidates), json.dumps(error)), flush=True)
            pathlib.Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2))
    policy = request()
    policy['require_verified'] = True
    policy['models'] = list(dict.fromkeys(result['model'] for result in policy['probe_results'] if result['status'] == 'supported'))
    request('', policy)
    report['finished_at'] = time.time()
    report['configured_models'] = policy['models']
    pathlib.Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2))
    passed = sum(result['status'] == 'supported' for result in report['results'])
    print('Configured verified-only selection: {} verified / {} completed, {} API errors'.format(passed, len(report['results']), len(report['errors'])), flush=True)
    return 1 if report['errors'] else 0


if __name__ == '__main__':
    raise SystemExit(main())
