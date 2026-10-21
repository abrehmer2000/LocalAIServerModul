#!/usr/bin/env python3
"""Real CLI enrollment, mTLS, managed inference and revocation with two local agents."""
import argparse
import hashlib
import json
from pathlib import Path
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--gateway', type=Path, required=True)
    parser.add_argument('--engine', type=Path, required=True)
    parser.add_argument('--model', type=Path, required=True)
    parser.add_argument('--outbound', action='store_true', help='use agent-initiated HTTPS only; no agent listener')
    args = parser.parse_args()
    binary, engine, model = [str(p.resolve(strict=True)) for p in (args.gateway, args.engine, args.model)]
    digest = hashlib.sha256()
    with open(model, 'rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            digest.update(chunk)
    processes, logs, engine_ports, agent_processes = [], [], [], []
    with tempfile.TemporaryDirectory(prefix='ki-basis-agent-smoke-') as temp:
        root = Path(temp)
        try:
            def start(arguments, name):
                log = (root / (name + '.log')).open('wb')
                logs.append(log)
                p = subprocess.Popen([binary, *arguments], stdout=log, stderr=subprocess.STDOUT)
                processes.append(p)
                return p

            def command(*arguments, **kwargs):
                return subprocess.check_output([binary, *arguments], **kwargs)

            pki = root / 'pki'
            command('init-tls', '--dir', str(pki), '--host', '127.0.0.1')
            ca = str(pki / 'ca.crt')
            tls_context = ssl.create_default_context(cafile=ca)
            gateway_port = free_port()
            base = f'https://127.0.0.1:{gateway_port}'
            key = json.loads(command('keygen'))
            config = {
                'listen': f'127.0.0.1:{gateway_port}', 'database': str(root / 'gateway.db'),
                'tls_cert': str(pki / 'gateway.crt'), 'tls_key': str(pki / 'gateway.key'),
                'control_ca_cert': ca, 'control_ca_key': str(pki / 'ca.key'),
                'keys': [{'id': 'smoke-app', 'sha256': key['sha256']}], 'nodes': [],
                'timeout_seconds': 60, 'max_body_bytes': 1048576, 'max_output_tokens': 64,
            }
            config_path = root / 'config.json'
            config_path.write_text(json.dumps(config), encoding='utf-8')
            gateway_process = start(['serve', '--config', str(config_path)], 'gateway')
            deadline = time.monotonic() + 15
            while True:
                try:
                    with urllib.request.urlopen(base + '/healthz', context=tls_context, timeout=2):
                        break
                except (urllib.error.URLError, TimeoutError):
                    if time.monotonic() > deadline or gateway_process.poll() is not None:
                        raise RuntimeError('Gateway did not start')
                    time.sleep(0.1)

            def audit_events():
                return [json.loads(line)['event'] for line in command('audit-export', '--config', str(config_path)).splitlines()]

            def chat(expect=200):
                req = urllib.request.Request(base + '/v1/chat/completions', data=json.dumps({
                    'model': 'chat-local', 'messages': [{'role': 'user', 'content': 'Once upon a time'}],
                    'stream': True, 'max_tokens': 24,
                }).encode(), headers={'Authorization': 'Bearer ' + key['key'], 'X-User-ID': 'agent-smoke-user', 'Content-Type': 'application/json'})
                try:
                    with urllib.request.urlopen(req, context=tls_context, timeout=70) as response:
                        data = response.read()
                        if expect != 200 or b'[DONE]' not in data:
                            raise AssertionError('Unexpected or incomplete inference')
                        request_id = response.headers['X-Request-ID']
                    deadline = time.monotonic() + 5
                    while time.monotonic() < deadline:
                        matches = [e for e in audit_events() if e.get('request_id') == request_id and e['type'] == 'inference.completed']
                        if matches:
                            return matches[0]['node_id']
                        time.sleep(0.05)
                    raise AssertionError('Completion not audited')
                except urllib.error.HTTPError as e:
                    if e.code != expect:
                        raise
                    return None

            for i in range(2):
                agent_port, engine_port = free_port(), free_port()
                engine_ports.append(engine_port)
                token = json.loads(command('join-token', '--config', str(config_path), '--connection', 'outbound' if args.outbound else 'direct', '--node-id', f'agent-{i+1}',
                    '--url', f'https://127.0.0.1:{agent_port}', '--model', 'chat-local', '--upstream-model', 'local-model',
                    '--revision', 'sha256:' + digest.hexdigest(), '--model-sha256', digest.hexdigest()))['token']
                state_path = str(root / f'agent-{i+1}.json')
                command('agent', 'join', '--gateway', base, '--ca', ca, '--state', state_path, '--token-stdin', input=token.encode())
                agent_processes.append(start(['agent', 'run', '--state', state_path, '--listen', f'127.0.0.1:{agent_port}',
                    '--engine', f'http://127.0.0.1:{engine_port}', '--engine-binary', engine,
                    '--model-file', model, '--context-size', '256'], f'agent-{i+1}'))
                deadline = time.monotonic() + 45
                while True:
                    if any(e['type'] == 'node.readiness_changed' and e.get('node_id') == f'agent-{i+1}' and e.get('outcome') == 'ready' for e in audit_events()):
                        break
                    if time.monotonic() > deadline:
                        raise RuntimeError('Agent did not become ready')
                    time.sleep(0.2)
                if i == 0:
                    assert chat() == 'agent-1'

            assert gateway_process.poll() is None
            nodes = {chat(), chat(), chat(), chat()}
            assert nodes == {'agent-1', 'agent-2'}, nodes
            ready_before = sum(e['type'] == 'node.readiness_changed' and e.get('outcome') == 'ready' for e in audit_events())
            gateway_process.terminate()
            gateway_process.wait(timeout=15)
            gateway_process = start(['serve', '--config', str(config_path)], 'gateway-restarted')
            deadline = time.monotonic() + 20
            while True:
                ready_after = sum(e['type'] == 'node.readiness_changed' and e.get('outcome') == 'ready' for e in audit_events())
                if ready_after >= ready_before + 2:
                    break
                if time.monotonic() > deadline:
                    raise AssertionError('Agents did not recover after gateway restart')
                time.sleep(0.2)
            assert {chat(), chat()} == {'agent-1', 'agent-2'}
            command('node-revoke', '--config', str(config_path), '--node-id', 'agent-1')
            assert chat() == 'agent-2'
            # Graceful agent exit withdraws readiness and stops its owned engine process.
            agent_processes[-1].terminate()
            agent_processes[-1].wait(timeout=15)
            chat(expect=503)
            try:
                with socket.create_connection(('127.0.0.1', engine_ports[-1]), timeout=1):
                    raise AssertionError('Owned engine still running after agent exit')
            except (ConnectionRefusedError, TimeoutError):
                pass
            events = audit_events()
            serialized = json.dumps(events)
            assert 'Once upon a time' not in serialized and key['key'] not in serialized
            assert all(token not in serialized for token in [token])
            proof = json.loads(command('audit-verify', '--config', str(config_path)))
            print(json.dumps({'outbound_only': args.outbound, 'dynamic_join': True, 'mtls': True, 'managed_engines': 2, 'join_without_gateway_restart': True, 'gateway_restart_recovery_verified': True,
                'inference_nodes': sorted(nodes), 'revocation_verified': True, 'agent_withdrawal_verified': True, 'engine_shutdown_verified': True,
                'audit': proof}, indent=2))
        except Exception:
            for p in root.glob('*.log'):
                # The agent and gateway never log tokens, keys or inference bodies.
                print(p.name + ':\n' + p.read_text(errors='replace')[-4000:])
            raise
        finally:
            for process in reversed(processes):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=15)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()
            for log in logs:
                log.close()


if __name__ == '__main__':
    main()
