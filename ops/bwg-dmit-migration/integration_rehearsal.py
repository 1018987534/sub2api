#!/usr/bin/env python3
"""Local disposable Docker fixtures. Never SSH; never use production containers."""
import contextlib
import importlib.util
import io
import json
import subprocess
import tempfile
import time
from types import SimpleNamespace
from pathlib import Path
from unittest.mock import patch

ROOT=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('migration_remote',ROOT/'remote.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
PG_IMAGE='public.ecr.aws/docker/library/postgres:18-alpine'
REDIS_IMAGE='public.ecr.aws/docker/library/redis:8-alpine'


def main():
    prefix='bwg-dmit-fixture-'+str(int(time.time()))
    containers=[]
    # Colima shares /Users, not the macOS /var/folders temporary tree.
    with tempfile.TemporaryDirectory(prefix=prefix, dir=str(Path.home())) as d:
        root=Path(d);m.STATE=root/'state';m.STATE.mkdir();m.PG=prefix+'-pg-target';m.REDIS=prefix+'-redis-target'
        def start(name,image,args,env=None,mount=None):
            cmd=['docker','run','-d','--name',name,'--network','none','--memory','384m','--security-opt','no-new-privileges:true']
            for k,v in (env or {}).items():cmd+=['-e',k+'='+v]
            if mount:cmd+=['-v',str(mount)+':/data']
            cmd += [image]+args
            m.run(cmd);containers.append(name)
        try:
            src=prefix+'-pg-source'
            start(src,PG_IMAGE,[],{'POSTGRES_USER':'sub2api','POSTGRES_DB':'sub2api','POSTGRES_PASSWORD':'local-fixture-only'})
            start(m.PG,PG_IMAGE,[],{'POSTGRES_USER':'migration_bootstrap','POSTGRES_DB':'postgres','POSTGRES_PASSWORD':'local-fixture-only'})
            for name in (src,m.PG):
                for _ in range(60):
                    if m.run(['docker','exec',name,'pg_isready','-U',m.env(m.inspect(name))['POSTGRES_USER']],check=False).returncode==0:break
                    time.sleep(1)
                else:raise RuntimeError('local PostgreSQL fixture not ready')
            m.sql(src,"""CREATE EXTENSION pg_trgm;
CREATE SCHEMA ops_migrations;
CREATE TABLE public.schema_migrations(version integer PRIMARY KEY);
INSERT INTO public.schema_migrations VALUES (1),(2);
CREATE TABLE public.accounts(id bigserial PRIMARY KEY,balance numeric(18,6),credentials jsonb,created_at timestamptz);
INSERT INTO accounts(balance,credentials,created_at) VALUES (12.345678,'{"fixture":"opaque encrypted value"}','2026-10-17T00:00:00Z'),(0,'{}','2026-10-17T03:00:00Z');
SELECT nextval('accounts_id_seq');
CREATE TABLE ops_migrations.audit(id int,remark text);
INSERT INTO ops_migrations.audit VALUES(1,'完整迁移');
CREATE ROLE fixture_reader;
GRANT USAGE ON SCHEMA public TO fixture_reader;
GRANT SELECT ON public.accounts TO fixture_reader;
SELECT lo_from_bytea(0,decode('012345','hex'));
""")
            globals_data=m.run(['docker','exec',src,'pg_dumpall','-U','sub2api','--globals-only']).stdout
            with patch('sys.stdin',io.StringIO(globals_data.decode())):m.globals_import(m.PG)
            m.db_reset(m.PG)
            data=m.run(['docker','exec',src,'pg_dump','-U','sub2api','-d','sub2api','-Fc','--create']).stdout
            with tempfile.TemporaryFile() as stream:
                stream.write(data);stream.seek(0)
                with patch('sys.stdin',io.TextIOWrapper(stream)):m.db_command('restore',m.PG)
            results=[]
            for name in (src,m.PG):
                out=io.StringIO()
                with contextlib.redirect_stdout(out):m.manifest(name)
                results.append(json.loads(out.getvalue()))
            if results[0]!=results[1]:
                for k in results[0]:
                    if results[0][k]!=results[1][k]:print('MISMATCH',k,repr(results[0][k]),repr(results[1][k]))
                raise RuntimeError('full fixture manifest mismatch')
            print('PostgreSQL: all schemas/rows/balances/sequences/roles/ACL/extensions/large objects MATCH')
            rs=prefix+'-redis-source'
            m.SOURCE_REDIS=rs
            for name in (rs,m.REDIS):
                mount=root/name;mount.mkdir()
                # Match the live source: stale REDISCLI_AUTH, server has no requirepass.
                args=['redis-server','--appendonly','yes']
                if name==m.REDIS:args+=['--requirepass','fixture-only']
                start(name,REDIS_IMAGE,args,{'REDISCLI_AUTH':'fixture-only'},mount)
                for _ in range(60):
                    try:
                        if m.redis_cli(name,'PING')=='PONG':break
                    except RuntimeError:pass
                    time.sleep(1)
                else:raise RuntimeError('Redis fixture did not become ready')
            m.redis_cli(rs,'SET','fixture:persistent','session-value')
            m.redis_cli(rs,'SET','fixture:ttl','rate-state','EX','3600')
            m.redis_cli(rs,'-n','1','SET','fixture:db1','other-db')
            expires=m.redis_cli(rs,'PEXPIRETIME','fixture:ttl')
            expected={key:m.redis_cli(rs,'GET',key) for key in ('fixture:persistent','fixture:ttl')}
            def transfer(source,target):
                # Only the app-stopped gate is mocked; the Docker persistence IO is real.
                with tempfile.TemporaryFile() as stream,patch.object(m,'require_local_apps_stopped'):
                    with patch('sys.stdout',SimpleNamespace(buffer=stream)):m.redis_export(source)
                    stream.seek(0)
                    with patch('sys.stdin',SimpleNamespace(buffer=stream)):m.redis_import(target)
            transfer(rs,m.REDIS)
            for key in ('fixture:persistent','fixture:ttl'):
                a=expected[key];b=m.redis_cli(m.REDIS,'GET',key)
                if a!=b:raise RuntimeError('Redis fixture value mismatch '+key+' source='+repr(a)+' target='+repr(b))
            if m.redis_cli(m.REDIS,'-n','1','GET','fixture:db1')!='other-db':raise RuntimeError('Redis DB1 lost')
            if m.redis_cli(m.REDIS,'PEXPIRETIME','fixture:ttl')!=expires:raise RuntimeError('Redis absolute expiry changed')
            ttl=int(m.redis_cli(m.REDIS,'TTL','fixture:ttl'))
            if not 3500<ttl<=3600:raise RuntimeError('Redis fixture TTL changed')
            m.run(['docker','restart',m.REDIS])
            if m.redis_cli(m.REDIS,'GET','fixture:persistent')!='session-value':raise RuntimeError('Redis did not persist imported data after restart')
            print('Redis: full RDB+AOF hashes MATCH; sessions, DB1 and absolute TTL MATCH; restart MATCH')
            m.redis_cli(m.REDIS,'SET','fixture:post-cutover','new-write')
            m.run(['docker','start',rs])
            transfer(m.REDIS,rs)
            if m.redis_cli(rs,'GET','fixture:post-cutover')!='new-write':raise RuntimeError('Redis reverse sync lost new writes')
            if m.redis_cli(rs,'PEXPIRETIME','fixture:ttl')!=expires:raise RuntimeError('Redis reverse sync changed TTL')
            m.run(['docker','restart',rs])
            if m.redis_cli(rs,'GET','fixture:post-cutover')!='new-write':raise RuntimeError('Redis reverse restart lost writes')
            print('Redis reverse-sync: newest target write retained; absolute TTL and restart MATCH')
            m.sql(m.PG,"INSERT INTO accounts(balance,credentials) VALUES(88.88,'{}')")
            globals_data=m.run(['docker','exec',m.PG,'pg_dumpall','-U','migration_bootstrap','--globals-only']).stdout
            with patch('sys.stdin',io.StringIO(globals_data.decode())):m.globals_import(src)
            m.PG=src
            m.db_reset(src)
            target=prefix+'-pg-target'
            data=m.run(['docker','exec',target,'pg_dump','-U','migration_bootstrap','-d','sub2api','-Fc','--create']).stdout
            with tempfile.TemporaryFile() as stream:
                stream.write(data);stream.seek(0)
                with patch('sys.stdin',SimpleNamespace(buffer=stream)):m.db_command('restore',src)
            reverse=[]
            for name in (src,target):
                out=io.StringIO()
                with contextlib.redirect_stdout(out):m.manifest(name)
                reverse.append(json.loads(out.getvalue()))
            if reverse[0]!=reverse[1]:raise RuntimeError('PostgreSQL reverse-sync manifest mismatch')
            print('PostgreSQL reverse-sync: newest target balance/write and complete manifest MATCH')
        finally:
            # Only names generated by this run are removed.
            for name in containers:
                m.run(['docker','rm','-f',name],check=False)


if __name__=='__main__':main()
