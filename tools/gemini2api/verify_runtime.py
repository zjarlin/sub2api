"""在候选容器中执行真实问答与拒绝边界验收。"""
import json
import os
import urllib.error
import urllib.request

opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
body = {'model': os.environ['G2A_MODELS'].split(',')[0], 'input': 'Reply with OK only.',
        'stream': True, 'max_output_tokens': 512, 'text': {'format': {'type': 'text'}}}


def call(payload):
    request = urllib.request.Request('http://127.0.0.1:7877/v1/responses', data=json.dumps(payload).encode(),
                                    headers={'Authorization': 'Bearer ' + os.environ['G2A_API_KEY'], 'Content-Type': 'application/json'})
    try:
        with opener.open(request, timeout=180) as response:
            return response.status, response.read().decode()
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode()


status, raw = call(body)
events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith('data: {')]
if status != 200 or not events or events[-1]['type'] != 'response.completed':
    raise RuntimeError('Real Gemini Responses test failed')
answer = ''.join(event.get('delta', '') for event in events if event['type'] == 'response.output_text.delta')
if 'OK' not in answer:
    raise RuntimeError('Unexpected answer')
for extra in ({'tools': [{'type': 'function', 'name': 'test'}]},
              {'input': [{'role': 'user', 'content': [{'type': 'input_image', 'image_url': 'https://example.com/a.png'}]}]}):
    if call(dict(body, **extra))[0] != 400:
        raise RuntimeError('Q&A restrictions failed')
print(json.dumps({'stream': 'response.completed', 'answer': answer, 'tools_http': 400, 'images_http': 400}))
