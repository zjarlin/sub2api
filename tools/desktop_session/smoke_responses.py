"""Real Responses tool roundtrip with a marker created only after the call."""
import argparse
import json
import urllib.error
import urllib.request
import uuid
from pathlib import Path


def send(base_url, key, body):
    request = urllib.request.Request(base_url.rstrip('/') + '/v1/responses',
                                     data=json.dumps(body).encode(), headers={
                                         'Authorization': 'Bearer ' + key, 'Content-Type': 'application/json'})
    # The desktop work model has a 600-second generation budget and buffers
    # output until validation. Allow that budget plus gateway overhead.
    with urllib.request.urlopen(request, timeout=660) as response:
        raw = response.read(4 * 1024 * 1024).decode()
    if not body.get('stream'):
        return json.loads(raw)
    events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith('data: {')]
    assert events and events[0]['type'] == 'response.created'
    assert events[-1]['type'] == 'response.completed'
    assert [event['sequence_number'] for event in events] == list(range(len(events)))
    return events[-1]['response']


def run(base_url, key, model):
    tool = {'type': 'function', 'name': 'lookup_marker', 'description': '读取调用方提供的未知随机标记',
            'parameters': {'type': 'object', 'properties': {}, 'additionalProperties': False}}
    first = send(base_url, key, {'model': model, 'input': '调用 lookup_marker 获取标记，收到结果后只回复实际标记。',
                                 'tools': [tool], 'tool_choice': 'required'})
    assert first['model'] == model
    calls = [item for item in first['output'] if item['type'] == 'function_call']
    assert len(calls) == 1 and calls[0]['name'] == 'lookup_marker'
    assert json.loads(calls[0]['arguments']) == {}
    marker = 'RESPONSES_' + uuid.uuid4().hex
    follow = {'model': model, 'previous_response_id': first['id'], 'stream': True,
              'input': [{'type': 'function_call_output', 'call_id': calls[0]['call_id'], 'output': marker}]}
    second = send(base_url, key, follow)
    assert second['model'] == model
    text = ''.join(part['text'] for item in second['output'] if item['type'] == 'message' for part in item['content'])
    assert text.strip() == marker
    retry = send(base_url, key, follow)
    assert retry['id'] == second['id'] and retry['output'] == second['output']
    return {'model': model, 'function_call_verified': True, 'client_result_exact_match': True,
            'responses_sse_verified': True, 'previous_response_id_verified': True, 'retry_replayed': True}


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--base-url', default='http://127.0.0.1:8080')
    parser.add_argument('--key-file', default='/run/secrets/api_key')
    parser.add_argument('--model', default='doubao-pro')
    args = parser.parse_args()
    try:
        print(json.dumps(run(args.base_url, Path(args.key_file).read_text().strip(), args.model)))
    except urllib.error.HTTPError as error:
        try:
            code = json.loads(error.read(8192)).get('error', {}).get('code')
        except (ValueError, AttributeError):
            code = None
        print(json.dumps({'http_status': error.code, 'error_code': code}))
        raise SystemExit(1)
