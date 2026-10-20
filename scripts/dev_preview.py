#!/usr/bin/env python3
"""Start a real local gateway and a read-only web preview on port 3000; no mock data."""
import argparse
import json
from pathlib import Path
import signal
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=Path('bin/ki-basis'))
    parser.add_argument('--port', type=int, default=3000)
    args = parser.parse_args()
    binary = str(args.binary.resolve(strict=True))
    root = Path('work/preview').resolve()
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    pki = root / 'pki'
    if not pki.exists():
        subprocess.run([binary, 'init-tls', '--dir', str(pki), '--host', '127.0.0.1'], check=True)
    access_path = root / 'access.json'
    if access_path.exists():
        access = json.loads(access_path.read_text())
    else:
        access = json.loads(subprocess.check_output([binary, 'keygen']))
        with access_path.open('x', encoding='utf-8') as file:
            access_path.chmod(0o600)
            json.dump(access, file)
    configuration = {
        'listen': '127.0.0.1:8443', 'database': str(root / 'gateway.db'),
        'tls_cert': str(pki / 'gateway.crt'), 'tls_key': str(pki / 'gateway.key'),
        'control_ca_cert': str(pki / 'ca.crt'), 'control_ca_key': str(pki / 'ca.key'),
        'keys': [{'id': 'preview-app', 'sha256': access['sha256']}], 'nodes': [],
    }
    config_path = root / 'config.json'
    config_path.write_text(json.dumps(configuration, indent=2), encoding='utf-8')
    processes = []
    logfiles = []
    stop = False

    def stop_handler(*_):
        nonlocal stop
        stop = True

    signal.signal(signal.SIGINT, stop_handler)
    signal.signal(signal.SIGTERM, stop_handler)
    try:
        for name, arguments in [
            ('gateway', ['serve', '--config', str(config_path)]),
            ('preview', ['web-preview', '--listen', f'0.0.0.0:{args.port}', '--backend', 'https://127.0.0.1:8443', '--ca', str(pki / 'ca.crt')]),
        ]:
            logfile = (root / (name + '.log')).open('ab')
            logfiles.append(logfile)
            processes.append(subprocess.Popen([binary, *arguments], stdout=logfile, stderr=subprocess.STDOUT))
        print(f'Webpreview: http://localhost:{args.port}', flush=True)
        print('Access key remains in work/preview/access.json; no models are installed automatically.', flush=True)
        while not stop:
            if any(process.poll() is not None for process in processes):
                raise RuntimeError('Preview process exited; inspect work/preview/*.log')
            time.sleep(0.25)
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
        for logfile in logfiles:
            logfile.close()


if __name__ == '__main__':
    main()
