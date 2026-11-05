import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('two_server_preflight.py')


class SourceReadinessTest(unittest.TestCase):
    def test_unpublished_features_block_without_importing_application(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'tools').mkdir()
            (root / 'navigator').mkdir()
            # Importing this fixture would fail; the check must only parse it.
            (root / 'llm.py').write_text('raise RuntimeError("must never import")\n', encoding='utf-8')
            (root / 'tools' / 'gateway_pruefen.py').write_text('', encoding='utf-8')
            (root / '.env').write_text('DO_NOT_PRINT_PRIVATE_VALUE', encoding='utf-8')
            process = subprocess.run([sys.executable, str(SCRIPT), '--prozesslog-dir', str(root)], capture_output=True, text=True)
            self.assertEqual(process.returncode, 1)
            report = json.loads(process.stdout)
            self.assertFalse(report['ready'])
            self.assertTrue(any(c['check'] == 'images_through_gateway' and c['status'] == 'fail' for c in report['checks']))
            self.assertNotIn('DO_NOT_PRINT_PRIVATE_VALUE', process.stdout + process.stderr)
            self.assertEqual(sorted(p.name for p in root.iterdir()), ['.env', 'llm.py', 'navigator', 'tools'])

    def test_missing_directory_has_machine_readable_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            process = subprocess.run([sys.executable, str(SCRIPT), '--prozesslog-dir', str(Path(directory) / 'missing')], capture_output=True, text=True)
            self.assertEqual(process.returncode, 1)
            self.assertFalse(json.loads(process.stdout)['ready'])


if __name__ == '__main__':
    unittest.main()
