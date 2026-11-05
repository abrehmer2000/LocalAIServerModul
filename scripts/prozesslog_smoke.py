#!/usr/bin/env python3
"""Real gateway checks using synthetic data only; never prints request/response content."""
import argparse
import base64
import concurrent.futures
import json
import os
import ssl
import struct
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def red_png():
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', zlib.crc32(kind + data))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!2I5B', 64, 64, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress((b'\0' + b'\xff\0\0' * 64) * 64)) + chunk(b'IEND', b''))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default=os.getenv('OPENAI_BASE_URL', ''), help='HTTPS API base ending in /v1')
    parser.add_argument('--model', default=os.getenv('OPENAI_MODEL', ''))
    parser.add_argument('--ca', default=os.getenv('OPENAI_CA_BUNDLE') or None)
    parser.add_argument('--user', default='prozesslog-synthetic-test')
    parser.add_argument('--parallel', type=int, default=4)
    parser.add_argument('--timeout', type=float, default=360)
    parser.add_argument('--report', help='write metadata-only JSON report (new file)')
    args = parser.parse_args()
    key = os.getenv('OPENAI_API_KEY', '')
    base = urllib.parse.urlsplit(args.base)
    if (base.scheme != 'https' or not base.hostname or base.username or base.password
            or base.query or base.fragment or not args.model or not key
            or not 1 <= args.parallel <= 16 or args.timeout <= 0):
        parser.error('HTTPS base, OPENAI_API_KEY, model and parallel=1..16 required; credentials belong in the environment')
    context = ssl.create_default_context(cafile=args.ca)
    endpoint = args.base.rstrip('/') + '/chat/completions'
    results = []

    def call(name, messages=None, barrier=None, **extra):
        body = dict(model=args.model, messages=messages or [{'role': 'user', 'content': 'Antworte nur mit OK.'}],
                    temperature=0, max_tokens=512, stream=False,
                    chat_template_kwargs={'enable_thinking': False})
        body.update(extra)
        request = urllib.request.Request(endpoint, json.dumps(body).encode(), headers={
            'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key, 'X-User-ID': args.user})
        opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPSHandler(context=context))
        if barrier:
            barrier.wait(timeout=10)
        start = time.monotonic()
        result = dict(check=name, status='fail')
        content = None
        try:
            with opener.open(request, timeout=args.timeout) as response:
                result['http_status'] = response.status
                obj = json.loads(response.read(16 * 1024 * 1024 + 1))
                content = obj['choices'][0]['message']['content']
                result['status'] = 'pass' if isinstance(content, str) and content.strip() else 'fail'
                if isinstance(content, str) and ('<think>' in content or '</think>' in content):
                    result['status'] = 'fail'
                usage = obj.get('usage') or {}
                for field in ('prompt_tokens', 'completion_tokens'):
                    if isinstance(usage.get(field), int):
                        result[field] = usage[field]
                fmt = extra.get('response_format', {}).get('type')
                if fmt in ('json_object', 'json_schema'):
                    value = json.loads(content)
                    if not isinstance(value, dict):
                        result['status'] = 'fail'
                    if fmt == 'json_schema' and (set(value) != {'answer'} or value['answer'] not in ('OK', None)):
                        result['status'] = 'fail'
        except urllib.error.HTTPError as error:
            result['http_status'] = error.code
            if error.code in (429, 503):
                result['retry_after'] = error.headers.get('Retry-After', '')
            error.close()
        except Exception as error:
            # Exception text can contain URLs, headers or generated contents.
            result['error_type'] = type(error).__name__
        result['duration_s'] = round(time.monotonic() - start, 3)
        return result, content

    def check(name, **kwargs):
        result, content = call(name, **kwargs)
        if name == 'image' and result['status'] == 'pass' and not any(color in content.lower() for color in ('rot', 'red')):
            result['status'] = 'fail'
        results.append(result)
        print(json.dumps(result), flush=True)

    check('chat')
    check('thinking', max_tokens=2048, chat_template_kwargs={'enable_thinking': True})
    check('json_object', messages=[{'role': 'user', 'content': 'Antworte als JSON-Objekt mit answer gleich OK.'}],
          response_format={'type': 'json_object'})
    check('json_schema', messages=[{'role': 'user', 'content': 'Antworte als JSON: answer ist OK.'}],
          response_format={'type': 'json_schema', 'json_schema': {'name': 'navigator_test', 'strict': True, 'schema': {
              'type': 'object', 'properties': {'answer': {'anyOf': [{'type': 'string', 'enum': ['OK']}, {'type': 'null'}]}},
              'required': ['answer'], 'additionalProperties': False}}})
    check('image', messages=[{'role': 'user', 'content': [
        {'type': 'text', 'text': 'Welche dominante Farbe hat dieses Bild? Antworte mit einem Wort.'},
        {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + base64.b64encode(red_png()).decode()}}]}])
    check('navigator_70000_chars', messages=[{'role': 'user', 'content':
          ('Dies ist synthetischer Kontext ohne Kundendaten. ' * 1500)[:70000] + '\nAntworte nur mit OK.'}])
    barrier = threading.Barrier(args.parallel)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.parallel) as pool:
        futures = [pool.submit(call, 'parallel_' + str(i + 1), barrier=barrier) for i in range(args.parallel)]
        for future in futures:
            result, _ = future.result()
            results.append(result)
            print(json.dumps(result), flush=True)
    if args.report:
        with open(args.report, 'x', encoding='utf-8') as handle:
            json.dump({'results': results}, handle, indent=2)
            handle.write('\n')
    return 0 if all(result['status'] == 'pass' for result in results) else 1


if __name__ == '__main__':
    raise SystemExit(main())
