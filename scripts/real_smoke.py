#!/usr/bin/env python3
"""Exercise two real llama-server instances through the compiled gateway (no mocks)."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def wait_ready(url, process, timeout=180):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('Process exited before becoming ready; inspect temporary logs')
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.2)
    raise TimeoutError(f'Readiness timeout: {url}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--gateway', required=True, type=Path)
    parser.add_argument('--engine', required=True, type=Path)
    parser.add_argument('--model', required=True, type=Path)
    parser.add_argument('--sdk', action='store_true', help='Use the official OpenAI Python SDK instead of urllib')
    args = parser.parse_args()
    gateway, engine, model = (p.resolve(strict=True) for p in (args.gateway, args.engine, args.model))
    digest = hashlib.sha256()
    with model.open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    processes, handles = [], []
    with tempfile.TemporaryDirectory(prefix='ki-basis-smoke-') as temp:
        root = Path(temp)
        try:
            ports = [port(), port(), port()]
            for i, engine_port in enumerate(ports[:2]):
                log = (root / f'engine-{i}.log').open('wb')
                handles.append(log)
                proc = subprocess.Popen([
                    str(engine), '--model', str(model), '--alias', 'local-model',
                    '--host', '127.0.0.1', '--port', str(engine_port),
                    '--ctx-size', '2048', '--parallel', '1', '--n-gpu-layers', '0',
                ], stdout=log, stderr=subprocess.STDOUT)
                processes.append(proc)
                wait_ready(f'http://127.0.0.1:{engine_port}/health', proc)
            key_data = json.loads(subprocess.check_output([str(gateway), 'keygen']))
            config = {
                'listen': f'127.0.0.1:{ports[2]}', 'database': str(root / 'audit.db'),
                'timeout_seconds': 180, 'max_body_bytes': 1048576, 'max_output_tokens': 128,
                'keys': [{'id': 'smoke-app', 'sha256': key_data['sha256']}],
                'nodes': [{
                    'id': f'node-{i + 1}', 'url': f'http://127.0.0.1:{engine_port}',
                    'model': 'chat-local', 'upstream_model': 'local-model',
                    'revision': digest.hexdigest(), 'capacity': 1,
                } for i, engine_port in enumerate(ports[:2])],
            }
            config_path = root / 'config.json'
            config_path.write_text(json.dumps(config), encoding='utf-8')
            log = (root / 'gateway.log').open('wb')
            handles.append(log)
            proc = subprocess.Popen([str(gateway), 'serve', '--config', str(config_path)], stdout=log, stderr=subprocess.STDOUT)
            processes.append(proc)
            base = f'http://127.0.0.1:{ports[2]}'
            wait_ready(base + '/healthz', proc)
            answers = []
            request_ids = []
            for i in range(2):
                body = json.dumps({'model': 'chat-local', 'messages': [
                    {'role': 'user', 'content': 'What is a local language model? Answer in one short sentence.'}
                ], 'stream': True, 'max_tokens': 48}).encode()
                req = urllib.request.Request(base + '/v1/chat/completions', data=body, headers={
                    'Authorization': 'Bearer ' + key_data['key'], 'X-User-ID': f'smoke-user-{i+1}',
                    'Content-Type': 'application/json',
                })
                answer, done = '', False
                if args.sdk:
                    from openai import OpenAI
                    with OpenAI(base_url=base + '/v1', api_key=key_data['key'], timeout=200, max_retries=0) as client:
                        stream = client.chat.completions.create(
                            model='chat-local', messages=[{'role': 'user', 'content': 'What is a local language model? Answer in one short sentence.'}],
                            stream=True, max_tokens=48, extra_headers={'X-User-ID': f'smoke-user-{i+1}'},
                        )
                        with stream:
                            request_ids.append(stream.response.headers['X-Request-ID'])
                            for chunk in stream:
                                for choice in chunk.choices:
                                    answer += choice.delta.content or ''
                                    if choice.finish_reason is not None:
                                        done = True
                else:
                    with urllib.request.urlopen(req, timeout=200) as response:
                        request_ids.append(response.headers['X-Request-ID'])
                        for raw in response:
                            if not raw.startswith(b'data: '):
                                continue
                            data = raw[6:].strip()
                            if data == b'[DONE]':
                                done = True
                                break
                            chunk = json.loads(data)
                            if 'error' in chunk:
                                raise RuntimeError('Gateway stream reported failure')
                            for choice in chunk.get('choices', []):
                                answer += choice.get('delta', {}).get('content', '') or ''
                if not done or not answer.strip():
                    raise RuntimeError('No complete, nonempty model response')
                answers.append(answer)
            completed = []
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                exported = subprocess.check_output([str(gateway), 'audit-export', '--config', str(config_path)])
                records = [json.loads(line) for line in exported.splitlines()]
                assert all('time' in r and r['time'].endswith('Z') for r in records)
                completed = [r['event'] for r in records if r['event']['type'] == 'inference.completed']
                if len(completed) == 2:
                    break
                time.sleep(0.1)
            assert {e['node_id'] for e in completed} == {'node-1', 'node-2'}
            assert {e['request_id'] for e in completed} == set(request_ids)
            assert {e['user_id'] for e in completed} == {'smoke-user-1', 'smoke-user-2'}
            assert key_data['key'].encode() not in exported
            assert b'What is a local language model?' not in exported
            verification = json.loads(subprocess.check_output([str(gateway), 'audit-verify', '--config', str(config_path)]))
            print(json.dumps({'real_inference': True, 'client': 'openai-python' if args.sdk else 'urllib', 'nodes_verified': 2, 'answers': answers, 'audit': verification}, ensure_ascii=False, indent=2))
        finally:
            for proc in reversed(processes):
                if proc.poll() is None:
                    proc.terminate()
                    try:
                        proc.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()
            for handle in handles:
                handle.close()


if __name__ == '__main__':
    main()
