"""Exercise postinst ordering with isolated account and service-manager fixtures."""
import io
import os
from pathlib import Path
import shlex
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent

class InstallGroupTests(unittest.TestCase):
    def run_install(self, *, existing=False, account=True, create_fails=False, lookup_fails=False, group_gid=823):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            app, fake_bin = base / 'app', base / 'bin'
            app.mkdir(); fake_bin.mkdir()
            (app / 'data').mkdir()
            (app / 'data/keep').write_text('user data')
            with tarfile.open(app / 'webui.bz2', 'w:bz2') as tar:
                data = b'frontend'
                info = tarfile.TarInfo('index.html'); info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
            marker, calls = base / 'group', base / 'calls'
            if existing:
                marker.touch()
            env = dict(os.environ, PATH=str(fake_bin) + os.pathsep + os.environ['PATH'], GROUP_MARKER=str(marker), TEST_CALLS=str(calls), TEST_GID=str(group_gid), ACCOUNT_READY='1' if account else '0')
            commands = {
                'id': '[ "$ACCOUNT_READY" = 1 ]',
                'getent': ('exit 3' if lookup_fails else '[ "$1" = group ] && [ "$2" = test-app ] || exit 1\n[ -f "$GROUP_MARKER" ] || exit 2\necho "test-app:x:$TEST_GID:"'),
                'groupadd': 'echo "groupadd $*" >> "$TEST_CALLS"\n' + ('exit 10' if create_fails else 'touch "$GROUP_MARKER"'),
                'systemctl': '[ -f "$GROUP_MARKER" ] || exit 98\necho "systemctl $*" >> "$TEST_CALLS"\nif [ "$1" = start ]; then [ "$ACCOUNT_READY" = 1 ] || exit 97; fi\nexit 0',
                'useradd': 'echo unexpected-useradd >> "$TEST_CALLS"; exit 99',
                'usermod': 'echo unexpected-usermod >> "$TEST_CALLS"; exit 99',
            }
            for name, body in commands.items():
                p = fake_bin / name
                p.write_text('#!/bin/sh\n' + body + '\n'); p.chmod(0o755)
            installer = base / 'postinst'
            installer.write_text((ROOT / 'packaging/debian/postinst.in').read_text().replace('@APP_ID@', 'test-app').replace('app=/usr/local/test-app', 'app=' + shlex.quote(str(app))))
            first = subprocess.run(['/bin/bash', str(installer), 'configure'], env=env, capture_output=True, text=True)
            first_calls = calls.read_text() if calls.exists() else ''
            second = None
            if first.returncode == 0:
                self.assertEqual((app / 'site/index.html').read_text(), 'frontend')
                (app / 'site/stale.js').write_text('old')
                env['ACCOUNT_READY'] = '1'
                second = subprocess.run(['/bin/bash', str(installer), 'configure', '0.1.0'], env=env, capture_output=True, text=True)
                self.assertFalse((app / 'site/stale.js').exists())
            self.assertEqual((app / 'data/keep').read_text(), 'user data')
            return first, second, first_calls, calls.read_text() if calls.exists() else ''

    def test_missing_group_created_before_service_and_preserved_on_upgrade(self):
        first, second, initial, calls = self.run_install()
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertTrue(initial.startswith('groupadd --system test-app\n'))
        self.assertEqual(calls.count('groupadd '), 1)
        self.assertEqual(calls.count('systemctl start test-app.service'), 2)
        self.assertNotIn('unexpected-', calls)

    def test_group_prepared_even_when_tos_user_is_created_later(self):
        first, second, initial, calls = self.run_install(account=False)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertIn('groupadd --system test-app', initial)
        self.assertIn('systemctl enable ', initial)
        self.assertNotIn('systemctl start ', initial)
        self.assertIn('waiting for the TOS application account', first.stderr)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(calls.count('systemctl start test-app.service'), 1)
        self.assertEqual(calls.count('groupadd '), 1)
        self.assertNotIn('unexpected-', calls)

    def test_existing_group_preserved(self):
        first, second, _, calls = self.run_install(existing=True)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertNotIn('groupadd', calls)

    def test_lookup_or_creation_failure_stops_before_service_registration(self):
        for args in ({'create_fails': True}, {'lookup_fails': True}):
            with self.subTest(args=args):
                first, second, _, calls = self.run_install(**args)
                self.assertNotEqual(first.returncode, 0)
                self.assertIsNone(second)
                self.assertNotIn('systemctl', calls)
                self.assertEqual(calls, 'groupadd --system test-app\n' if args.get('create_fails') else '')

    def test_root_group_is_rejected_before_service_registration(self):
        first, _, _, calls = self.run_install(existing=True, group_gid=0)
        self.assertNotEqual(first.returncode, 0)
        self.assertEqual(calls, '')

if __name__ == '__main__':
    unittest.main()
