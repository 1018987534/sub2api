#!/usr/bin/env python3
"""No network or production mutations. Exercise refusal gates and generated configs."""
import contextlib
import importlib.util
import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('migration_remote',ROOT/'remote.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)


class MigrationChecks(unittest.TestCase):
    def test_network_refuses_running_app(self):
        with tempfile.TemporaryDirectory() as d, patch.object(m,'STATE',Path(d)):
            (Path(d)/'run-id').write_text('20261017T000000Z-1\n')
            (Path(d)/'apps-stopped').write_text('20261017T000000Z-1\n')
            with patch.object(m,'inspect',return_value={'State':{'Running':True}}),patch.object(m,'run') as run:
                with self.assertRaisesRegex(RuntimeError,'still running'):
                    m.wg_apply('20261017T000000Z-1')
                run.assert_not_called()

    def test_network_refuses_stale_marker(self):
        with tempfile.TemporaryDirectory() as d, patch.object(m,'STATE',Path(d)):
            (Path(d)/'run-id').write_text('20261017T000000Z-1\n')
            (Path(d)/'apps-stopped').write_text('old-run\n')
            with patch.object(m,'run') as run:
                with self.assertRaisesRegex(RuntimeError,'stale'):
                    m.wg_apply('20261017T000000Z-1')
                run.assert_not_called()

    def test_refuse_reseed_after_target_write(self):
        with tempfile.TemporaryDirectory() as d, patch.object(m,'STATE',Path(d)),patch.object(m,'sql') as sql:
            (Path(d)/'target-writes-possible').write_text('run')
            with self.assertRaisesRegex(RuntimeError,'accepted writes'):
                m.db_reset(m.PG)
            sql.assert_not_called()

    def test_compose_preserves_real_project_and_relative_files(self):
        c={'Config':{'Labels':{'com.docker.compose.project':'sub2api-gateway-154',
           'com.docker.compose.project.working_dir':'/opt/sub2api-gateway-154',
           'com.docker.compose.project.config_files':'docker-compose.standalone.yml,docker-compose.gateway.override.yml'}}}
        with patch.object(m,'compose',return_value=['docker-compose']):
            a=m.compose_app(c)
        self.assertEqual(a[0],'docker-compose')
        self.assertEqual(a[a.index('-p')+1],'sub2api-gateway-154')
        self.assertIn('/opt/sub2api-gateway-154/docker-compose.standalone.yml',a)

    def test_globals_keep_password_hash_and_acl(self):
        raw='CREATE ROLE sub2api;\nALTER ROLE sub2api WITH LOGIN PASSWORD \'fixture-password-hash\';\nCREATE ROLE audit_reader;\nGRANT audit_reader TO sub2api;\n'
        with patch.object(m,'sql',return_value='sub2api'),patch.object(m,'pgargs',return_value=['psql']),patch.object(m,'run') as run,patch('sys.stdin',io.StringIO(raw)),contextlib.redirect_stdout(io.StringIO()):
            m.globals_import(m.PG)
            payload=run.call_args.args[1].decode()
        self.assertNotIn('CREATE ROLE sub2api;',payload)
        self.assertIn('fixture-password-hash',payload)
        self.assertIn('CREATE ROLE audit_reader;',payload)
        self.assertIn('GRANT audit_reader TO sub2api;',payload)

    def test_cli_requires_explicit_execute(self):
        for phase in ('prepare','stage','seed','cutover','wireguard','publish','finalize','rollback','close'):
            p=subprocess.run([str(ROOT/'bwg_to_dmit.sh'),phase],capture_output=True,text=True)
            self.assertNotEqual(p.returncode,0)
            self.assertIn('add --execute',p.stderr)

    def test_pipeline_failure_propagates(self):
        # Streaming export failure must not be hidden by a successful consumer.
        p=subprocess.run(['bash','-c','set -euo pipefail; (exit 42) | cat >/dev/null; echo UNSAFE'],capture_output=True,text=True)
        self.assertEqual(p.returncode,42)
        self.assertNotIn('UNSAFE',p.stdout)

    def test_compose_patch_is_scoped_and_persistent(self):
        content='services:\n  sub2api:\n    image: unchanged\n    environment:\n      - DATABASE_HOST=postgres\n      - DATABASE_MAX_OPEN_CONNS=${DATABASE_MAX_OPEN_CONNS:-64}\n      - INSTANCE_ROLE=gateway\n    volumes:\n      - /unchanged:/app/sub2api\n  postgres:\n    environment:\n      - DATABASE_HOST=keep-original\n'
        output,found=m.patch_compose_environment(content,m.app_changes('gateway'))
        self.assertIn('      - DATABASE_HOST=10.20.0.1\n',output)
        self.assertIn('      - DATABASE_MAX_OPEN_CONNS=12\n',output)
        self.assertIn('      - DATABASE_HOST=keep-original\n',output)
        self.assertIn('/unchanged:/app/sub2api',output)
        self.assertEqual(found,{'DATABASE_HOST','DATABASE_MAX_OPEN_CONNS','INSTANCE_ROLE'})
        self.assertEqual(m.patch_compose_environment(output,m.app_changes('gateway'))[0],output)

    def test_compose_patch_mapping_syntax(self):
        content='services:\n  sub2api:\n    environment:\n      DATABASE_HOST: old\n      INSTANCE_ROLE: control\n'
        output,found=m.patch_compose_environment(content,m.app_changes('control'))
        self.assertIn('DATABASE_HOST: 10.20.0.1\n',output)
        self.assertEqual(found,{'DATABASE_HOST','INSTANCE_ROLE'})

    def test_redis_auth_fallback_unsets_not_empty_auth(self):
        auth=subprocess.CompletedProcess([],0,b'PONG\n',b'AUTH failed: no password configured')
        ok=subprocess.CompletedProcess([],0,b'PONG\n',b'')
        with patch.object(m,'run',side_effect=[auth,ok]) as run:
            self.assertEqual(m.redis_cli('fixture','PING'),'PONG')
            self.assertIn('env',run.call_args.args[0])
            self.assertIn('-u',run.call_args.args[0])

    def test_redis_auth_errors_never_count_as_success(self):
        error=subprocess.CompletedProcess([],0,b'NOAUTH Authentication required.\n',b'')
        with patch.object(m,'run',return_value=error):
            with self.assertRaisesRegex(RuntimeError,'authentication failed'):m.redis_cli('fixture','DBSIZE')

    def test_redis_requires_stopped_application_before_any_io(self):
        with patch.object(m,'require_local_apps_stopped',side_effect=RuntimeError('app still running')),patch.object(m,'inspect') as inspect:
            with self.assertRaisesRegex(RuntimeError,'still running'):m.redis_import(m.REDIS)
            inspect.assert_not_called()

    def test_reverse_globals_do_not_create_temporary_admin(self):
        raw='CREATE ROLE migration_bootstrap;\nALTER ROLE migration_bootstrap WITH LOGIN SUPERUSER;\nCREATE ROLE sub2api;\n'
        with patch.object(m,'require_local_apps_stopped'),patch.object(m,'sql',return_value='sub2api'),patch.object(m,'pgargs',return_value=['psql']),patch.object(m,'run') as run,patch('sys.stdin',io.StringIO(raw)),contextlib.redirect_stdout(io.StringIO()):
            m.globals_import('sub2api-postgres')
            self.assertNotIn('migration_bootstrap',run.call_args.args[1].decode())

    def test_failure_retains_private_diagnostics_without_printing_secret(self):
        error=subprocess.CompletedProcess([],42,b'',b'fixture-private-diagnostic')
        with tempfile.TemporaryDirectory() as d,patch.object(m,'STATE',Path(d)):
            with self.assertRaisesRegex(RuntimeError,'exit 42') as raised:m.command_failed('fixture-operation',error)
            self.assertNotIn('fixture-private-diagnostic',str(raised.exception))
            record=json.loads((Path(d)/'last-error.json').read_text())
            self.assertEqual(record['exit_code'],42)
            self.assertEqual(record['stderr'],'fixture-private-diagnostic')
            self.assertEqual((Path(d)/'last-error.json').stat().st_mode & 0o777,0o600)


if __name__=='__main__':unittest.main(verbosity=2)
