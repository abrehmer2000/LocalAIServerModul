#!/usr/bin/env python3
"""Collect dependency license notices for the developer binary package."""
import argparse
import json
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--go', default='go')
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
raw = subprocess.check_output([args.go, 'list', '-m', '-json', 'all'], text=True)
decoder = json.JSONDecoder()
go_root=Path(subprocess.check_output([args.go,'env','GOROOT'],text=True).strip())
licenses = ['Go runtime / LICENSE\n\n'+(go_root/'LICENSE').read_text()]
while raw.strip():
    raw = raw.lstrip()
    module, end = decoder.raw_decode(raw)
    raw = raw[end:]
    if module.get('Main') or not module.get('Dir'):
        continue
    directory = Path(module['Dir'])
    files = sorted({p for pattern in ['LICENSE*', 'COPYING*', 'NOTICE*', 'AUTHORS*'] for p in directory.glob(pattern) if p.is_file()})
    for file in files:
        licenses.append('\n' + '=' * 72 + '\n' + module['Path'] + ' ' + module.get('Version', '') + ' / ' + file.name + '\n\n' + file.read_text(errors='replace'))
args.output.write_text('Third-party notices for KI-Basis development build\n' + '\n'.join(licenses), encoding='utf-8')
