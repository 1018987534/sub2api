"""Exercise generation safety without touching the operator checkout."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('sync_openai_models.py')
SPEC = json.loads(SCRIPT.with_name('openai-supplemental.json').read_text())


class GenerationTests(unittest.TestCase):
    def test_preview_write_drift_and_unrelated_prices(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            pricing = root / 'backend/resources/model-pricing/model_prices_and_context_window.json'
            pricing.parent.mkdir(parents=True)
            pricing.write_text('{\n  "other-model": {\n    "input_cost_per_token": 123\n  }\n}\n')
            manifest = root / 'models.json'
            manifest.write_text(json.dumps(SPEC))
            command = [sys.executable, str(SCRIPT), '--repo', str(root), '--manifest', str(manifest)]
            def run(*args):
                return subprocess.run(command + list(args), capture_output=True, text=True)
            before = pricing.read_bytes()
            self.assertEqual(run().returncode, 0)
            self.assertEqual(before, pricing.read_bytes())
            self.assertEqual(run('--check').returncode, 1)
            self.assertEqual(run('--write').returncode, 0)
            self.assertEqual(run('--check').returncode, 0)
            after = pricing.read_bytes()
            self.assertEqual(run('--write').returncode, 0)
            self.assertEqual(after, pricing.read_bytes())
            result = json.loads(after)
            self.assertEqual(result['other-model'], {'input_cost_per_token': 123})
            self.assertEqual(result['gpt-6-sol']['cache_creation_input_token_cost'], 0)
            for key in ['cache_write_policy', 'flex_multiplier', 'reasoning']:
                invalid = copy.deepcopy(SPEC)
                invalid['models'][0][key] = 'unreviewed'
                manifest.write_text(json.dumps(invalid))
                self.assertEqual(run('--write').returncode, 2)
                self.assertEqual(after, pricing.read_bytes())


if __name__ == '__main__':
    unittest.main()
