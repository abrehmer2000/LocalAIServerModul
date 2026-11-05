#!/usr/bin/env python3
"""Read-only source readiness check; never imports Prozesslog or reads its secrets/database."""
import argparse
import ast
import json
from pathlib import Path
import subprocess
import sys


def literals(paths):
    found = set()
    for path in paths:
        tree = ast.parse(path.read_text(encoding='utf-8'), filename=path.name)
        found.update(node.value for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str))
    return found


def inspect(root):
    checks = []

    def add(name, passed):
        checks.append({'check': name, 'status': 'pass' if passed else 'fail'})

    try:
        model = literals([root / 'llm.py'])
        navigator = literals(sorted((root / 'navigator').glob('*.py')))
        probe = literals([root / 'tools' / 'gateway_pruefen.py'])
    except (OSError, SyntaxError, UnicodeError):
        add('prozesslog_sources_readable', False)
        return {'checks': checks, 'ready': False}
    add('thinking_per_task', 'aufgabe' in model)
    add('images_through_gateway', 'OPENAI_BILDER' in model)
    add('navigator_through_gateway', 'RAG_NAVIGATOR_GATEWAY' in navigator | model)
    add('gateway_probe_thinking', '--denken' in probe)
    add('gateway_probe_image', '--bild' in probe)
    add('windows_installer', (root / 'deploy' / 'windows' / 'installieren.ps1').is_file())
    add('windows_readiness_tool', (root / 'tools' / 'bereit_pruefen.py').is_file())
    result = {'checks': checks, 'ready': all(check['status'] == 'pass' for check in checks)}
    # Optional provenance only; source copies without .git can still be checked.
    try:
        git = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=root, capture_output=True, text=True, timeout=5, check=True)
        result['prozesslog_commit'] = git.stdout.strip()
    except (OSError, subprocess.SubprocessError):
        result['prozesslog_commit'] = 'unavailable'
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prozesslog-dir', type=Path, required=True)
    args = parser.parse_args(argv)
    result = inspect(args.prozesslog_dir.resolve())
    print(json.dumps(result, indent=2))
    print('Code readiness only: run the real gateway and Prozesslog readiness checks before testing.', file=sys.stderr)
    return 0 if result['ready'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
