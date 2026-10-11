"""TeamCity 部署 Gemini 问答适配器，验收后切换既有账号。"""
import fcntl
import json
import os
import pathlib
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request


def command(args, **kwargs):
    return subprocess.check_output(args, encoding='utf-8', **kwargs).strip()


def query(sql):
    return command(['docker', 'exec', '-i', 'sub2api-postgres', 'sh', '-c',
                    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'], input=sql)


def private_file(path, text):
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as file:
        file.write(text)


def main():
    account_id = int(os.environ.get('GEMINI_QA_ACCOUNT_ID', '885'))
    sha = os.environ['GEMINI_QA_REVISION']
    if len(sha) != 40 or any(c not in '0123456789abcdef' for c in sha):
        raise ValueError('GEMINI_QA_REVISION must be a full Git SHA')
    root = pathlib.Path(os.environ.get('DEPLOY_DIR', '/opt/sub2api')) / 'gemini-qa'
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    with open(str(root / 'deploy.lock'), 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        deploy(account_id, sha, root)


def deploy(account_id, sha, root):
    original = json.loads(query('SELECT row_to_json(a) FROM accounts a WHERE id={} AND deleted_at IS NULL;'.format(account_id)))
    if original['platform'] != 'openai' or original['type'] != 'apikey':
        raise ValueError('Target account must be an OpenAI API key account')
    credentials = original['credentials']
    mapping = credentials.get('model_mapping', {})
    models = sorted(set(mapping.values()))
    if not models or any(not isinstance(m, str) or not m.startswith('gemini-') or any(c in m for c in ',\r\n') for m in models):
        raise ValueError('Configure explicit Gemini model mappings before deployment')
    prior_runtime = root / 'runtime.json'
    prior = json.loads(prior_runtime.read_text()) if prior_runtime.exists() else None
    if credentials.get('base_url', '').startswith('https://generativelanguage.googleapis.com/'):
        gemini_key = credentials['api_key']
    elif prior and credentials.get('base_url') == prior.get('base_url'):
        gemini_key = prior['gemini_key']
    else:
        raise ValueError('Cannot resolve Gemini credential from this account')
    admin_key = query("SELECT value FROM settings WHERE key='admin_api_key';")
    group_key = query("SELECT k.key FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.group_id=6 AND k.status='active' AND k.deleted_at IS NULL AND u.role='admin' ORDER BY k.id LIMIT 1;")
    if not admin_key or not group_key:
        raise ValueError('Admin authentication and an admin-owned codex group key are required')
    network = os.environ.get('G2A_DOCKER_NETWORK', 'sub2api_sub2api-network')
    command(['docker', 'network', 'inspect', network])
    image = 'zjarlin/gemini-qa:' + sha[:12]
    candidate = 'gemini-qa-' + sha[:12] + '-' + secrets.token_hex(3)
    adapter_key = secrets.token_urlsafe(32)
    release = root / candidate
    release.mkdir(mode=0o700)
    private_file(release / 'original-account.json', json.dumps(original))
    env = {'G2A_API_KEY': adapter_key, 'GEMINI_API_KEY': gemini_key,
           'G2A_MODELS': ','.join(models), 'G2A_LISTEN': '0.0.0.0:7877',
           'G2A_UPSTREAM_PROXY': os.environ.get('G2A_UPSTREAM_PROXY', 'http://sub2api-openai-egress:7890')}
    if any('\n' in v or '\r' in v for v in env.values()):
        raise ValueError('Invalid environment value')
    private_file(release / 'runtime.env', ''.join(k + '=' + v + '\n' for k, v in env.items()))
    subprocess.check_call(['docker', 'build', '--pull=false', '-t', image, str(pathlib.Path(__file__).parent)])
    gateway = os.environ.get('SUB2API_URL', 'http://127.0.0.1:18080').rstrip('/')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def request(path, body=None, key=admin_key, admin=True):
        headers = {'Content-Type': 'application/json'}
        headers['x-api-key' if admin else 'Authorization'] = key if admin else 'Bearer ' + key
        req = urllib.request.Request(gateway + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
        if admin and body is not None:
            req.method = 'PUT'
        with opener.open(req, timeout=180) as response:
            return json.load(response)

    started = False
    switched = False
    try:
        command(['docker', 'run', '-d', '--name', candidate, '--network', network,
                 '--env-file', str(release / 'runtime.env'), '--restart', 'unless-stopped',
                 '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
                 '--health-cmd', "python -c \"import urllib.request; urllib.request.urlopen('http://127.0.0.1:7877/livez',timeout=5).read()\"",
                 '--health-interval', '10s', '--health-timeout', '5s', '--health-retries', '3', image])
        started = True
        for _ in range(30):
            health = command(['docker', 'inspect', candidate, '--format', '{{.State.Health.Status}}'])
            if health == 'healthy':
                break
            time.sleep(1)
        else:
            raise RuntimeError('Adapter health check timed out')
        check = pathlib.Path(__file__).with_name('verify_runtime.py').read_text(encoding='utf-8')
        print(command(['docker', 'exec', '-i', candidate, 'python', '-'], input=check))
        updated_credentials = dict(credentials, base_url='http://' + candidate + ':7877', api_key=adapter_key)
        extra = dict(original.get('extra') or {}, openai_responses_supported=True,
                     openai_responses_mode='force_responses', openai_apikey_responses_websockets_v2_enabled=False,
                     openai_apikey_responses_websockets_v2_mode='off')
        # 清除账号上的出口代理；Gemini 上游代理由适配器自己处理。
        switched = True
        request('/api/v1/admin/accounts/' + str(account_id),
                {'credentials': updated_credentials, 'extra': extra, 'proxy_id': 0})
        baseline = int(query('SELECT COALESCE(MAX(id),0) FROM usage_logs;'))
        response = request('/v1/responses', {'model': next(iter(mapping)), 'input': 'Reply with OK only.',
                           'max_output_tokens': 512, 'stream': False}, key=group_key, admin=False)
        answer = ''.join(part.get('text', '') for item in response.get('output', []) for part in item.get('content', []))
        if response.get('status') != 'completed' or 'OK' not in answer:
            raise RuntimeError('Codex group Responses acceptance failed')
        hit = ''
        for _ in range(10):
            hit = query("SELECT account_id FROM usage_logs WHERE id>{} AND api_key_id=(SELECT id FROM api_keys WHERE key='{}') AND model='{}' ORDER BY id DESC LIMIT 1;".format(baseline, group_key.replace("'", "''"), next(iter(mapping)).replace("'", "''")))
            if hit:
                break
            time.sleep(1)
        if hit != str(account_id):
            raise RuntimeError('Codex group test did not select the target account')
        runtime = {'container': candidate, 'image': image, 'revision': sha,
                   'base_url': updated_credentials['base_url'], 'gemini_key': gemini_key}
        private_file(prior_runtime, json.dumps(runtime))
        public = {k: v for k, v in runtime.items() if k != 'gemini_key'}
        public.update(account_id=account_id, group_id=6, answer=answer, status='completed')
        private_file(release / 'acceptance.json', json.dumps(public))
        print('DEPLOYED ' + json.dumps(public))
    except Exception:
        if switched:
            request('/api/v1/admin/accounts/' + str(account_id),
                    {'credentials': original['credentials'], 'extra': original.get('extra') or {}, 'proxy_id': original.get('proxy_id') or 0})
            print('Original account configuration restored')
        if started:
            command(['docker', 'update', '--restart', 'no', candidate])
            command(['docker', 'stop', candidate])
        raise
    # 上次实例保留作回退，不自动删除。


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('Gemini deployment failed: ' + type(error).__name__, file=sys.stderr)
        sys.exit(1)
