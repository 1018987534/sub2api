import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('mirror', Path(__file__).resolve().parents[1] / 'sync_client_downloads.py')
mirror = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(mirror)


class MirrorTransactionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.payload = bytes.fromhex('d0cf11e0a1b11ae1') + b'installer fixture'
        self.spec = {'name': 'CC-Switch-v1.2.3-Windows.msi', 'source': 'https://github.com/farion1231/cc-switch/releases/download/v1.2.3/CC-Switch-v1.2.3-Windows.msi', 'bytes': len(self.payload), 'sha256': hashlib.sha256(self.payload).hexdigest()}

    def tearDown(self):
        self.temp.cleanup()

    def run_publish(self, plan):
        with patch.object(mirror, 'request', side_effect=lambda *args: io.BytesIO(self.payload)):
            mirror.publish(self.root, 'v1.2.3', plan)

    def test_complete_release_publishes_readable_files_and_reuses_verified_installers(self):
        self.run_publish({'ccswitch-windows-x64': self.spec})
        manifest_path = self.root / 'manifest.json'
        before = json.loads(manifest_path.read_text())
        file = self.root / before['files']['ccswitch-windows-x64']['url'].removeprefix('/downloads/clients/')
        self.assertEqual(file.read_bytes(), self.payload)
        self.assertEqual(file.parent.stat().st_mode & 0o777, 0o755)
        with patch.object(mirror, 'request', side_effect=AssertionError('must not download unchanged files')):
            mirror.publish(self.root, 'v1.2.3', {'ccswitch-windows-x64': self.spec})
        after = json.loads(manifest_path.read_text())
        self.assertEqual(before['files'], after['files'])
        self.assertIn('checked_at', after)

    def test_bad_checksum_preserves_previous_manifest_and_removes_partial_files(self):
        self.run_publish({'ccswitch-windows-x64': self.spec})
        before = (self.root / 'manifest.json').read_bytes()
        invalid = {**self.spec, 'name': 'bad.msi', 'sha256': '0' * 64}
        with self.assertRaisesRegex(ValueError, 'SHA-256'):
            self.run_publish({'ccswitch-windows-x64': invalid})
        self.assertEqual((self.root / 'manifest.json').read_bytes(), before)
        self.assertFalse(list(self.root.glob('.pending-*')))

    def test_failure_on_second_file_does_not_publish_half_of_a_release(self):
        self.run_publish({'ccswitch-windows-x64': self.spec})
        before = (self.root / 'manifest.json').read_bytes()
        invalid = {**self.spec, 'name': 'second.msi', 'bytes': len(self.payload) + 100}
        with self.assertRaisesRegex(ValueError, 'Incomplete'):
            self.run_publish({'ccswitch-windows-x64': self.spec, 'ccswitch-windows-arm64': invalid})
        self.assertEqual((self.root / 'manifest.json').read_bytes(), before)

    def test_html_error_page_cannot_be_published_as_installer(self):
        fake = b'<html>access denied</html>'
        spec = {**self.spec, 'bytes': len(fake), 'sha256': hashlib.sha256(fake).hexdigest()}
        with patch.object(mirror, 'request', return_value=io.BytesIO(fake)):
            with self.assertRaisesRegex(ValueError, 'MSI'):
                mirror.publish(self.root, 'v1.2.3', {'ccswitch-windows-x64': spec})
        self.assertFalse((self.root / 'manifest.json').exists())

    def test_other_upstream_hosts_are_rejected(self):
        with self.assertRaisesRegex(ValueError, 'upstream'):
            mirror.request('https://example.com/installer.msi')


if __name__ == '__main__':
    unittest.main()
