#!/usr/bin/env python3
"""Import explicit per-model probe results into account extra (no account cooldown changes)."""
import argparse
import json
import re
import subprocess


def definitive_result(row):
    if row.get('result') == 'supported':
        return 'supported'
    # 限流、余额、无渠道、未配价格和普通 404 均不能证明模型不受支持。
    message = row.get('error', '').lower()
    if row.get('http_status') not in (400, 404, 410, 422):
        return None
    markers = ('invalid model format', 'model not found', 'model_not_found',
               'model is not available', 'model not available', 'model is not found', 'unknown model',
               'unsupported model', 'does not support this model')
    missing_model = re.search(r"requested model '[^']+' is not available", message)
    return 'unsupported' if missing_model or any(marker in message for marker in markers) else None


def snapshots(paths):
    combined = {}
    for path in paths:
        with open(path, encoding='utf-8') as source:
            for account in json.load(source)['accounts']:
                key = (int(account['id']), account['base_url'].rstrip('/'))
                snapshot = combined.setdefault(key, {
                    'base_url': key[1], 'checked_at': account['checked_at'], 'models': {}})
                snapshot['checked_at'] = max(snapshot['checked_at'], account['checked_at'])
                for row in account.get('models', []):
                    status = definitive_result(row)
                    if status:
                        snapshot['models'][row['model']] = status
    return {key: value for key, value in combined.items() if value['models']}


def quote(value):
    return "'" + value.replace("'", "''") + "'"


def statement(account_id, snapshot):
    encoded = quote(json.dumps(snapshot, ensure_ascii=True)) + '::jsonb'
    # 身份、地址和时间均由导入数据与本地库核对；只合并同地址的模型结论。
    return """UPDATE accounts SET extra = COALESCE(extra,'{{}}'::jsonb) ||
jsonb_build_object('model_route_probes', {snapshot} || jsonb_build_object('models',
 CASE WHEN extra->'model_route_probes'->>'base_url' = {base}
 THEN COALESCE(extra->'model_route_probes'->'models','{{}}'::jsonb)
 ELSE '{{}}'::jsonb END || (({snapshot})->'models'))), updated_at=NOW()
WHERE id={account_id} AND deleted_at IS NULL AND type='apikey'
AND rtrim(credentials->>'base_url','/')={base}
AND (extra->'model_route_probes'->>'checked_at' IS NULL
 OR (extra->'model_route_probes'->>'checked_at')::timestamptz <= {checked}::timestamptz);""".format(
        snapshot=encoded, base=quote(snapshot['base_url']), account_id=int(account_id),
        checked=quote(snapshot['checked_at']))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('results', nargs='+', help='JSON with accounts[].models[] probe outcomes')
    parser.add_argument('--apply', action='store_true', help='Write evidence to this database')
    parser.add_argument('--sql', action='store_true', help='Emit SQL for review or a rollback test')
    parser.add_argument('--container', default='sub2api-postgres')
    parser.add_argument('--database', default='sub2api')
    parser.add_argument('--user', default='sub2api')
    args = parser.parse_args()
    evidence = snapshots(args.results)
    sql = '\n'.join(statement(account_id, snapshot)
                    for (account_id, _), snapshot in sorted(evidence.items()))
    if args.sql:
        print(sql)
    elif args.apply:
        subprocess.run(['docker', 'exec', '-i', args.container, 'psql', '-X',
                        '-v', 'ON_ERROR_STOP=1', '-U', args.user, '-d', args.database],
                       input=('BEGIN;\n' + sql + '\nCOMMIT;').encode('utf-8'), check=True)
    else:
        print(json.dumps([{'account_id': key[0], **snapshot}
                          for key, snapshot in sorted(evidence.items())], indent=2))


if __name__ == '__main__':
    main()
