#!/usr/bin/env python3
"""Remote helper. JSON diagnostics are redacted; data streams only go to SSH pipes."""
import hashlib
import io
import json
import os
import re
import shlex
import shutil
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path

STATE = Path('/run/bwg-dmit-migration')
DATA = Path('/opt/sub2api-migration-staging')
DOMAINS = ('check.nideyiyi.com', 'check.xiaofanqie.org')
PG = 'sub2api-migration-postgres'
REDIS = 'sub2api-migration-redis'
SOURCE_REDIS = 'sub2api-redis'
BOOTSTRAP = 'migration_bootstrap'


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def command_failed(operation, result):
    message = '%s failed (exit %d)' % (operation, result.returncode)
    if STATE.is_dir():
        # SQL/Docker errors may contain credentials or row values. Retain them
        # only in root-private runtime state; never send raw diagnostics to logs.
        save('last-error.json', {'operation': operation, 'exit_code': result.returncode,
                                'stdout': (result.stdout or b'').decode(errors='replace'),
                                'stderr': (result.stderr or b'').decode(errors='replace')})
        message += '; private diagnostics: ' + str(STATE / 'last-error.json')
    raise RuntimeError(message)


def run(args, data=None, check=True):
    p = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and p.returncode:
        # Commands may contain secrets. Neither argv nor raw Docker/SQL errors are logged.
        command_failed(args[0], p)
    return p


def text(args):
    return run(args).stdout.decode().strip()


def inspect(name):
    return json.loads(text(['docker', 'inspect', name]))[0]


def env(c):
    return dict(s.split('=', 1) for s in c['Config'].get('Env', []) if '=' in s)


def write(path, content, mode=0o600):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    tmp = path.with_name(path.name + '.next')
    tmp.write_bytes(content.encode() if isinstance(content, str) else content)
    tmp.chmod(mode)
    tmp.replace(path)


def save(name, obj):
    write(STATE / name, json.dumps(obj, sort_keys=True))


def read(name):
    return json.loads((STATE / name).read_text())


def sha(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def compose():
    if run(['docker', 'compose', 'version'], check=False).returncode == 0:
        return ['docker', 'compose']
    require(shutil.which('docker-compose'), 'no Compose v1/v2 installed')
    return ['docker-compose']


def compose_app(c, override=None, config_files=None):
    labels = c['Config']['Labels']
    wd = labels['com.docker.compose.project.working_dir']
    args = compose() + ['-p', labels['com.docker.compose.project']]
    for name in (config_files if config_files is not None else labels['com.docker.compose.project.config_files'].split(',')):
        args += ['-f', str(Path(wd) / name)]
    if override:
        args += ['-f', str(override)]
    return args + ['--project-directory', wd, '--env-file', str(Path(wd) / '.env')]


def stage_compose(*args):
    return compose() + ['-p', 'sub2api-migration', '-f', str(DATA / 'compose.json')] + list(args)


def pgargs(container, db='sub2api'):
    user = env(inspect(container))['POSTGRES_USER']
    return ['docker', 'exec', '-i', '-e', 'PGOPTIONS=-c max_parallel_workers_per_gather=0 -c timezone=UTC -c datestyle=ISO', container,
            'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-U', user, '-d', db, '-At']


def sql(container, query, db='sub2api'):
    return text(pgargs(container, db) + ['-c', query])


def identifier(s):
    return '"' + s.replace('"', '""') + '"'


def literal(s):
    return "'" + s.replace("'", "''") + "'"


def current_run(run_id):
    require((STATE / 'run-id').read_text().strip() == run_id, 'run-id mismatch')


def mark(name, run_id):
    current_run(run_id)
    write(STATE / name, run_id + '\n')


def stopped(run_id):
    current_run(run_id)
    require((STATE / 'apps-stopped').read_text().strip() == run_id, 'apps-stopped marker absent/stale')
    require(not inspect('sub2api')['State']['Running'], 'Sub2API is still running')


def inventory(expected):
    c = inspect('sub2api')
    e = env(c)
    ready = json.loads(text(['curl', '-fsS', '--max-time', '15', 'http://127.0.0.1:8080/health/ready']))
    require(ready.get('instance_id') == expected and ready.get('status') == 'ready', 'unexpected/not-ready application')
    keys = ('DATABASE_HOST', 'DATABASE_PORT', 'DATABASE_DBNAME', 'DATABASE_USER', 'REDIS_HOST', 'REDIS_PORT',
            'INSTANCE_ID', 'INSTANCE_ROLE', 'DATABASE_MAX_OPEN_CONNS', 'DATABASE_MAX_IDLE_CONNS')
    result = dict(host=os.uname().nodename, architecture=os.uname().machine, ready=ready,
                  app_env={k: e.get(k) for k in keys}, compose=compose(),
                  app_project=c['Config']['Labels']['com.docker.compose.project'],
                  app_binary_sha256=text(['docker', 'exec', 'sub2api', 'sha256sum', '/app/sub2api']).split()[0],
                  free=text(['free', '-b']), disk=text(['df', '-B1', '/']),
                  wg_address=json.loads(text(['ip', '-j', '-4', 'addr', 'show', 'wg0'])),
                  wg_endpoints=text(['wg', 'show', 'wg0', 'endpoints']),
                  wg_handshakes=text(['wg', 'show', 'wg0', 'latest-handshakes']),
                  containers=text(['docker', 'ps', '--format', '{{.Names}}\t{{.Image}}']),
                  secret_fingerprints={k: hashlib.sha256(e[k].encode()).hexdigest()
                                       for k in ('DATABASE_PASSWORD', 'REDIS_PASSWORD', 'JWT_SECRET', 'TOTP_ENCRYPTION_KEY') if e.get(k)})
    if expected == 'bwg-us-01' and e.get('DATABASE_HOST') in ('postgres', 'sub2api-postgres', '127.0.0.1', 'localhost'):
        result['database'] = sql('sub2api-postgres', "SELECT json_build_object('bytes',pg_database_size(current_database()),'public_tables',(SELECT count(*) FROM pg_tables WHERE schemaname='public'),'migrations',(SELECT count(*) FROM schema_migrations),'version',current_setting('server_version'))")
        result['schemas'] = sql('sub2api-postgres', "SELECT nspname FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname <> 'information_schema' ORDER BY 1")
        result['roles'] = sql('sub2api-postgres', "SELECT rolname FROM pg_roles WHERE rolname !~ '^pg_' ORDER BY 1")
        require(sql('sub2api-postgres', "SELECT count(*) FROM pg_tablespace WHERE spcname NOT IN ('pg_default','pg_global')") == '0', 'custom tablespace requires a reviewed target path')
    print(json.dumps(result, sort_keys=True))


def snapshot(run_id):
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    if (STATE / 'run-id').exists():
        current_run(run_id)
        drift_guard(run_id)
        print('snapshot=reused')
        return
    c = inspect('sub2api')
    require(c['State']['Running'], 'baseline app must be running')
    save('app.inspect.json', c)
    wd = c['Config']['Labels']['com.docker.compose.project.working_dir']
    shutil.copy2(Path(wd) / '.env', STATE / 'app.env.before')
    (STATE / 'app.env.before').chmod(0o600)
    shutil.copy2('/etc/wireguard/wg0.conf', STATE / 'wg0.conf.before')
    save('app.baseline.json', {'binary': text(['docker', 'exec', 'sub2api', 'sha256sum', '/app/sub2api']).split()[0],
                             'wg_enabled': text(['systemctl', 'is-enabled', 'wg-quick@wg0'],) if run(['systemctl', 'is-enabled', 'wg-quick@wg0'], check=False).returncode == 0 else 'disabled'})
    # Capture only hashes of mutable input files; never send their contents to logs.
    paths = [str(Path(wd) / f) for f in c['Config']['Labels']['com.docker.compose.project.config_files'].split(',')]
    changes = app_changes(env(c)['INSTANCE_ROLE'])
    found = set()
    for i, p in enumerate(paths):
        content = Path(p).read_text()
        shutil.copy2(p, STATE / ('compose-before-' + str(i)))
        _, keys = patch_compose_environment(content, changes)
        found.update(keys)
    require(set(changes) <= found, 'unsupported Compose environment layout; review before cutover')
    compose_probe(c)
    save('baseline-hashes.json', {p: sha(p) for p in paths + [str(Path(wd) / '.env'), '/etc/wireguard/wg0.conf']})
    write(STATE / 'run-id', run_id + '\n')
    print('snapshot=ready')


def drift_guard(run_id):
    current_run(run_id)
    for p, digest in read('baseline-hashes.json').items():
        require(sha(p) == digest, 'live config changed after stage: ' + p + '; reprepare before stopping apps')
    require(text(['docker', 'exec', 'sub2api', 'sha256sum', '/app/sub2api']).split()[0] == read('app.baseline.json')['binary'], 'application binary changed since stage')


def compose_probe(c=None):
    c = c or inspect('sub2api')
    labels = c['Config']['Labels']; wd = Path(labels['com.docker.compose.project.working_dir'])
    changes = app_changes(env(c)['INSTANCE_ROLE'])
    # Temporary config files only; no container or live config is changed.
    with tempfile.TemporaryDirectory(prefix='bwg-dmit-compose-') as d:
        paths = []; found = set()
        for i, name in enumerate(labels['com.docker.compose.project.config_files'].split(',')):
            content, keys = patch_compose_environment((wd / name).read_text(), changes)
            found.update(keys)
            path = Path(d) / ('compose-' + str(i) + '.yaml'); write(path, content); paths.append(str(path))
        require(set(changes) <= found, 'Compose environment fields missing')
        args = compose_app(c, config_files=paths)
        if args[0] == 'docker':
            config = json.loads(text(args + ['config', '--format', 'json']))
        else:
            import yaml
            config = yaml.safe_load(text(args + ['config']))
        actual = config['services']['sub2api']['environment']
        require(all(str(actual.get(k)) == v for k, v in changes.items()), 'merged Compose environment differs from migration plan')
    print('compose-preview=valid; role/DB/Redis/pool fields verified; live configuration unchanged')


def escaped(obj):
    if isinstance(obj, str):
        return obj.replace('$', '$$')
    if isinstance(obj, dict):
        return {k: escaped(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [escaped(x) for x in obj]
    return obj


def source_services():
    require(env(inspect('sub2api'))['INSTANCE_ID'] == 'bwg-us-01', 'source services must be inspected on BWG')
    services = [inspect(n) for n in ('sub2api-postgres', 'sub2api-redis', 'komari')]
    pg = services[0]; user = env(pg)['POSTGRES_USER']
    pg['_migration_timezone'] = text(['docker', 'exec', 'sub2api-postgres', 'psql', '-X', '-q', '-At', '-U', user, '-d', 'sub2api', '-c', 'SHOW timezone'])
    print(json.dumps(services))


def set_stage_timezone(config, timezone):
    require(re.fullmatch(r'[A-Za-z0-9_+./-]+', timezone), 'unexpected source PostgreSQL timezone')
    pg = config['services']['postgres']; command = pg['command']
    command = [value for value in command]
    for i, value in enumerate(command):
        if value.startswith('timezone='): command[i] = 'timezone=' + timezone; break
    else: command += ['-c', 'timezone=' + timezone]
    pg['command'] = command; pg['environment']['TZ'] = timezone


def stage(run_id):
    current_run(run_id)
    source = {c['Name'].lstrip('/'): c for c in json.load(sys.stdin)}
    timezone = source['sub2api-postgres']['_migration_timezone']
    if DATA.exists():
        require((STATE / 'source-services.json').exists() and (DATA / 'compose.json').exists(), 'target data root not owned by this prepared run')
        require(not (STATE / 'target-writes-possible').exists(), 'target may have accepted writes; refusing to restage')
        old = read('source-services.json')
        require(all(source[n]['Image'] == old[n]['Image'] for n in source), 'source data image changed after stage; review compatibility before reusing restored data')
        config = json.loads((DATA / 'compose.json').read_text())
        set_stage_timezone(config, timezone)
        write(DATA / 'compose.json', json.dumps(config, indent=2))
        save('source-services.json', source)
        print('stage-config=reused; source PostgreSQL timezone=' + timezone)
        return
    app_env = env(inspect('sub2api'))
    require(app_env['DATABASE_DBNAME'] == 'sub2api', 'unexpected database name')
    require(int(text(['df', '-B1', '--output=avail', '/']).splitlines()[-1]) > 16 * 1024**3, 'less than 16 GiB free before stage')
    DATA.mkdir(mode=0o700)
    for d in ('postgres_data', 'redis_data', 'komari_data'):
        (DATA / d).mkdir(mode=0o700)
    services = {
        'postgres': dict(image=source['sub2api-postgres']['Image'], container_name=PG, restart='unless-stopped',
                         environment={'POSTGRES_USER': BOOTSTRAP, 'POSTGRES_DB': 'postgres', 'POSTGRES_PASSWORD': app_env['DATABASE_PASSWORD'], 'PGDATA': '/var/lib/postgresql/data'},
                         volumes=[str(DATA / 'postgres_data') + ':/var/lib/postgresql/data'], ports=['127.0.0.1:15432:5432'],
                         command=['postgres', '-c', 'shared_buffers=128MB', '-c', 'work_mem=2MB', '-c', 'maintenance_work_mem=64MB', '-c', 'max_connections=100', '-c', 'max_parallel_workers_per_gather=0'],
                         mem_limit='768m', healthcheck={'test': ['CMD', 'pg_isready', '-U', BOOTSTRAP, '-d', 'postgres'], 'interval': '5s', 'timeout': '3s', 'retries': 30}),
        'redis': dict(image=source['sub2api-redis']['Image'], container_name=REDIS, restart='unless-stopped',
                      environment={'REDISCLI_AUTH': app_env.get('REDIS_PASSWORD', '')},
                      volumes=[str(DATA / 'redis_data') + ':/data'], ports=['127.0.0.1:16379:6379'],
                      command=['redis-server', '--appendonly', 'yes', '--appendfsync', 'everysec', '--save', '60 1', '--requirepass', app_env.get('REDIS_PASSWORD', '')],
                      mem_limit='384m', healthcheck={'test': ['CMD-SHELL', 'redis-cli ping | grep -qx PONG'], 'interval': '5s', 'timeout': '3s', 'retries': 30}),
        'komari': dict(image=source['komari']['Image'], container_name='komari', restart='unless-stopped',
                       environment={k: v for k, v in env(source['komari']).items() if k.startswith(('KOMARI_', 'GIN_'))},
                       volumes=[str(DATA / 'komari_data') + ':/app/data'], ports=['10.20.0.1:25774:25774'], mem_limit='192m')}
    for service in services.values():
        service['pull_policy'] = 'never'
        service['security_opt'] = ['no-new-privileges:true']
        service['logging'] = {'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '2'}}
    config = {'services': services}; set_stage_timezone(config, timezone)
    write(DATA / 'compose.json', json.dumps(escaped(config), indent=2))
    save('source-services.json', source)
    print('stage-config=ready; image IDs pinned to source; ports=loopback')


def wait_health(containers):
    for c in containers:
        for _ in range(60):
            if inspect(c)['State'].get('Health', {}).get('Status') == 'healthy':
                break
            time.sleep(2)
        else:
            raise RuntimeError(c + ' not healthy')


def db_command(action, container):
    user = env(inspect(container))['POSTGRES_USER']
    if action == 'globals-export':
        args = ['docker', 'exec', container, 'pg_dumpall', '-U', user, '--globals-only']
    elif action == 'dump':
        args = ['docker', 'exec', container, 'pg_dump', '-U', user, '-d', 'sub2api', '-Fc', '--create']
    elif action == 'schema-export':
        args = ['docker', 'exec', '-e', 'PGOPTIONS=-c timezone=UTC', container, 'pg_dump', '-U', user, '-d', 'sub2api', '--schema-only']
    elif action == 'restore':
        args = ['docker', 'exec', '-i', container, 'pg_restore', '-U', user, '-d', 'postgres', '--create', '--exit-on-error']
    else:
        raise RuntimeError('unsupported stream')
    p = subprocess.run(args, stdin=sys.stdin.buffer if action == 'restore' else subprocess.DEVNULL,
                       stdout=sys.stdout.buffer if action != 'restore' else subprocess.DEVNULL,
                       stderr=subprocess.PIPE)
    if p.returncode: command_failed(action, p)


def globals_import(container):
    if container == 'sub2api-postgres': require_local_apps_stopped()
    raw = sys.stdin.read()
    # The temporary restore administrator is not a business role and must not
    # be introduced on BWG during reverse sync.
    raw = re.sub(r'^(?:CREATE|ALTER) ROLE migration_bootstrap[^\n]*;\n?', '', raw, flags=re.M)
    existing = set(sql(container, 'SELECT rolname FROM pg_roles', 'postgres').splitlines())
    def create(match):
        name = match.group(1)
        decoded = name[1:-1].replace('""', '"') if name.startswith('"') else name
        return '' if decoded in existing else match.group(0)
    raw = re.sub(r'^CREATE ROLE (.+);$', create, raw, flags=re.M)
    run(pgargs(container, 'postgres'), raw.encode())
    print('globals=imported (password hashes and grants retained)')


def db_reset(container):
    require(container in (PG, 'sub2api-postgres'), 'refuse unknown database target')
    if container == 'sub2api-postgres':
        require_local_apps_stopped()
    else:
        require(not (STATE / 'target-writes-possible').exists(), 'target has accepted writes; refuse destructive reseed')
    sql(container, 'DROP DATABASE IF EXISTS sub2api WITH (FORCE)', 'postgres')


def schema_text(container, database='sub2api'):
    user = env(inspect(container))['POSTGRES_USER']
    schema = text(['docker', 'exec', '-e', 'PGOPTIONS=-c timezone=UTC', container, 'pg_dump', '-U', user, '-d', database, '--schema-only'])
    return '\n'.join(line for line in schema.splitlines() if not line.startswith(('\\restrict ', '\\unrestrict ')))


def schema_normalize(container):
    require(container == PG, 'schema normalization is restricted to the isolated DMIT PostgreSQL container')
    raw = sys.stdin.buffer.read()
    database = 'bwg_dmit_schema_' + str(os.getpid()) + '_' + str(time.time_ns())
    sql(container, 'CREATE DATABASE ' + identifier(database) + ' TEMPLATE template0', 'postgres')
    try:
        # Older PostgreSQL catalogs can deparse varchar-array casts differently
        # after restore. Reparse BOTH complete schemas on the same server and
        # in UTC, retaining every constraint, index, policy, owner and ACL.
        if re.search(rb'^CREATE SCHEMA public;', raw, re.M):
            sql(container, 'DROP SCHEMA public', database)
        run(pgargs(container, database), raw)
        print(hashlib.sha256(schema_text(container, database).encode()).hexdigest())
    finally:
        sql(container, 'DROP DATABASE ' + identifier(database) + ' WITH (FORCE)', 'postgres')


def manifest(container, only_table=None):
    # Hash every row independent of physical order; bounded client memory.
    tables = json.loads(sql(container, "SELECT coalesce(json_agg(x ORDER BY n,c),'[]') FROM (SELECT n.nspname n,c.relname c FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','m') AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema') x"))
    if only_table:
        tables = [t for t in tables if t['n']=='public' and t['c']==only_table]
        require(tables, 'probe table absent')
    output = {'tables': []}
    for table in tables:
        relation = identifier(table['n']) + '.' + identifier(table['c'])
        query = 'COPY (SELECT md5(row_to_json(t)::text) FROM ONLY ' + relation + ' t ORDER BY md5(row_to_json(t)::text) COLLATE "C") TO STDOUT'
        p = subprocess.Popen(pgargs(container) + ['-c', query], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        digest = hashlib.sha256()
        rows = 0
        for chunk in iter(lambda: p.stdout.read(1024 * 1024), b''):
            digest.update(chunk)
            rows += chunk.count(b'\n')
        exit_code = p.wait()
        if exit_code:
            command_failed('table digest ' + relation, subprocess.CompletedProcess([], exit_code, b'', p.stderr.read()))
        output['tables'].append(dict(schema=table['n'], name=table['c'], rows=rows, sha256=digest.hexdigest()))
    sequences = json.loads(sql(container, "SELECT coalesce(json_agg(x ORDER BY n,c),'[]') FROM (SELECT n.nspname n,c.relname c FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='S' AND n.nspname !~ '^pg_') x"))
    output['sequences'] = [{**s, 'state': sql(container, 'SELECT last_value,is_called FROM ' + identifier(s['n']) + '.' + identifier(s['c']))} for s in sequences]
    output['extensions'] = sql(container, 'SELECT extname,extversion FROM pg_extension ORDER BY 1')
    output['role_memberships'] = sql(container, 'SELECT r.rolname,m.rolname,a.admin_option,a.inherit_option,a.set_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member ORDER BY 1,2')
    output['database_attributes'] = sql(container, "SELECT pg_encoding_to_char(encoding),datcollate,datctype,datlocprovider,datlocale,coalesce(datacl::text,'') FROM pg_database WHERE datname=current_database()")
    output['roles'] = sql(container, "SELECT rolname,rolsuper,rolinherit,rolcreaterole,rolcreatedb,rolcanlogin,rolreplication,rolconnlimit,rolvaliduntil,rolbypassrls,md5(coalesce(rolpassword,'')) FROM pg_authid WHERE rolname !~ '^pg_' AND rolname <> 'migration_bootstrap' ORDER BY 1")
    output['large_objects'] = sql(container, "SELECT count(*),coalesce(md5(string_agg(loid::text||':'||pageno::text||':'||md5(data::text),'' ORDER BY loid,pageno)),'') FROM pg_largeobject")
    schema = schema_text(container)
    output['schema_sha256'] = hashlib.sha256(schema.encode()).hexdigest()
    print(json.dumps(output, sort_keys=True, indent=2))


def stop_app(run_id):
    current_run(run_id)
    c = inspect('sub2api')
    run(['docker', 'update', '--restart=no', 'sub2api'])
    if c['State']['Running']:
        run(['docker', 'stop', '-t', '90', 'sub2api'])
        require(inspect('sub2api')['State'].get('ExitCode') != 137, 'app exceeded graceful stop window; inspect in-flight requests before proceeding')
    mark('apps-stopped', run_id)
    print('application=stopped; marker=apps-stopped')


def wg_prepare(run_id, address, endpoint, peers_json):
    current_run(run_id)
    stopped(run_id)
    old = (STATE / 'wg0.conf.before').read_text()
    match = re.search(r'^PrivateKey\s*=\s*(\S+)', old, re.M)
    require(match, 'private key missing in original wg0 config')
    lines = ['[Interface]', 'Address = ' + address, 'PrivateKey = ' + match.group(1)]
    if endpoint == 'hub':
        lines += ['ListenPort = 51820']
    for peer in json.loads(peers_json):
        lines += ['', '[Peer]', 'PublicKey = ' + peer['key'], 'AllowedIPs = ' + peer['address'] + '/32']
        if endpoint != 'hub':
            lines += ['Endpoint = ' + endpoint, 'PersistentKeepalive = 25']
    write(STATE / 'next/wg0.conf', '\n'.join(lines) + '\n')


def wg_apply(run_id, restore=False):
    stopped(run_id)
    path = STATE / ('wg0.conf.before' if restore else 'next/wg0.conf')
    # The SSH transport uses public addresses and is not attached to wg0.
    if run(['ip', 'link', 'show', 'wg0'], check=False).returncode == 0:
        run(['wg-quick', 'down', 'wg0'])
    shutil.copy2(path, '/etc/wireguard/wg0.conf')
    run(['wg-quick', 'up', 'wg0'])
    if restore and read('app.baseline.json')['wg_enabled'] != 'enabled':
        run(['systemctl', 'disable', 'wg-quick@wg0'])
    else:
        run(['systemctl', 'enable', 'wg-quick@wg0'])
    print('wireguard=' + ('restored' if restore else 'moved'))


def relay(action):
    path = Path('/usr/local/sbin/vmiss-wireguard-relay')
    before = STATE / 'relay.before'
    if action == 'apply':
        require(not before.exists(), 'relay already changed')
        content = path.read_text()
        require('center=95.169.18.157' in content, 'unexpected relay upstream')
        shutil.copy2(path, before)
        run(['systemctl', 'stop', 'vmiss-wireguard-relay'])
        write(path, content.replace('center=95.169.18.157', 'center=179.255.105.184'), 0o755)
        run(['systemctl', 'start', 'vmiss-wireguard-relay'])
    elif before.exists():
        run(['systemctl', 'stop', 'vmiss-wireguard-relay'])
        shutil.copy2(before, path)
        run(['systemctl', 'start', 'vmiss-wireguard-relay'])
    print('YT-relay=' + action)


def app_changes(role):
    return {'DATABASE_HOST': '10.20.0.1', 'DATABASE_PORT': '5432', 'REDIS_HOST': '10.20.0.1', 'REDIS_PORT': '6379',
            'INSTANCE_ROLE': role, 'DATABASE_MAX_OPEN_CONNS': '16' if role == 'control' else '12',
            'DATABASE_MAX_IDLE_CONNS': '4' if role == 'control' else '2'}


def patch_compose_environment(content, changes):
    # Narrow YAML edit: only sub2api.environment, retaining comments, mounts,
    # images, other services and the exact live Compose project/file set.
    lines = content.splitlines(keepends=True); result = []; service = False; environment = False
    service_indent = environment_indent = None; found = set()
    for line in lines:
        stripped = line.lstrip(); indent = len(line) - len(stripped)
        significant = stripped.strip() and not stripped.startswith('#')
        if significant and service and indent <= service_indent:
            service = environment = False
        if re.match(r'^\s+sub2api:\s*(?:#.*)?$', line):
            service = True; service_indent = indent
        if significant and environment and indent <= environment_indent:
            environment = False
        if service and re.match(r'^\s+environment:\s*(?:#.*)?$', line):
            environment = True; environment_indent = indent
        elif environment:
            for key, value in changes.items():
                match = re.match(r'^(\s*-\s*)' + re.escape(key) + r'=.*?(\r?\n)?$', line)
                mapping = re.match(r'^(\s*)' + re.escape(key) + r':\s*.*?(\r?\n)?$', line)
                if match or mapping:
                    m = match or mapping; separator = '=' if match else ': '
                    line = m.group(1) + key + separator + value + (m.group(2) or '')
                    found.add(key); break
        result.append(line)
    return ''.join(result), found


def app_start(run_id, role, restore=False):
    current_run(run_id)
    original = read('app.inspect.json')
    wd = Path(original['Config']['Labels']['com.docker.compose.project.working_dir'])
    files = [Path(wd) / f for f in original['Config']['Labels']['com.docker.compose.project.config_files'].split(',')]
    if restore:
        shutil.copy2(STATE / 'app.env.before', wd / '.env')
        for i, p in enumerate(files): shutil.copy2(STATE / ('compose-before-' + str(i)), p)
        args = compose_app(original)
    else:
        changes = app_changes(role)
        lines = (STATE / 'app.env.before').read_text().splitlines()
        lines = [line for line in lines if line.split('=', 1)[0] not in changes]
        write(wd / '.env', '\n'.join(lines) + '\n' + ''.join(k + '=' + v + '\n' for k, v in changes.items()))
        found = set()
        for i, p in enumerate(files):
            content, keys = patch_compose_environment((STATE / ('compose-before-' + str(i))).read_text(), changes)
            found.update(keys)
            write(p, content)
        require(set(changes) <= found, 'Compose environment fields missing')
        args = compose_app(original)
        # Mark BEFORE start: startup/background tasks may write even without public traffic.
        mark('target-writes-possible', run_id)
    run(args + ['up', '-d', '--no-deps', '--force-recreate', 'sub2api'])
    for _ in range(60):
        p = run(['curl', '-fsS', '--max-time', '5', 'http://127.0.0.1:8080/health/ready'], check=False)
        if p.returncode == 0:
            ready = json.loads(p.stdout)
            require(ready.get('role') == role and ready.get('instance_id') == env(original)['INSTANCE_ID'], 'role or instance ID changed')
            live = inspect('sub2api')
            expected_env = {k: env(original).get(k) for k in app_changes(role)} if restore else app_changes(role)
            require(all(env(live).get(k) == value for k, value in expected_env.items()), 'live application data/pool configuration differs')
            require(live['Image'] == original['Image'], 'application image changed')
            require(sorted((m['Source'], m['Destination']) for m in live['Mounts']) == sorted((m['Source'], m['Destination']) for m in original['Mounts']), 'application mounts changed')
            require('no-new-privileges:true' in (live['HostConfig'].get('SecurityOpt') or []), 'security flag missing')
            require(text(['docker', 'exec', 'sub2api', 'sha256sum', '/app/sub2api']).split()[0] == read('app.baseline.json')['binary'], 'application binary changed')
            (STATE / 'apps-stopped').unlink(missing_ok=True)
            print(json.dumps(ready, sort_keys=True))
            return
        time.sleep(2)
    raise RuntimeError('application failed readiness; stop and run rollback, with --reverse-sync if DMIT has started')


def promote():
    cfg = json.loads((DATA / 'compose.json').read_text())
    cfg['services']['postgres']['ports'] = ['10.20.0.1:5432:5432']
    cfg['services']['redis']['ports'] = ['10.20.0.1:6379:6379']
    write(DATA / 'compose.json', json.dumps(cfg, indent=2))
    run(stage_compose('up', '-d', 'postgres', 'redis'))
    wait_health([PG, REDIS])
    print('data-listeners=10.20.0.1 only')


def redis_export(container):
    require(container in (REDIS, SOURCE_REDIS), 'unknown Redis source')
    require_local_apps_stopped()
    c = inspect(container)
    root = Path(next(m['Source'] for m in c['Mounts'] if m['Destination'] == '/data'))
    if c['State']['Running']:
        config = redis_config(container)
        require(redis_cli(container, 'SAVE') == 'OK', 'Redis SAVE failed')
        run(['docker', 'stop', '-t', '60', container])
        require(inspect(container)['State'].get('ExitCode') != 137, 'Redis graceful stop timed out')
    else:
        previous = read('redis-export-manifest.json')
        require(file_manifest(root) == previous['files'], 'stopped Redis changed after export')
        config = previous['config']
    files = file_manifest(root)
    require(config['dbfilename'] in files, 'Redis RDB missing after SAVE')
    require(any(n.startswith(config['appenddirname'] + '/') for n in files), 'Redis AOF missing')
    manifest = {'format': 1, 'config': config, 'files': files}
    save('redis-export-manifest.json', manifest)
    payload = json.dumps(manifest, sort_keys=True).encode()
    with tarfile.open(fileobj=sys.stdout.buffer, mode='w|') as archive:
        member = tarfile.TarInfo('migration-manifest.json'); member.size = len(payload); member.mode = 0o600
        archive.addfile(member, io.BytesIO(payload))
        for name in sorted(files):
            archive.add(root / name, arcname='data/' + name, recursive=False)


def redis_import(container):
    require(container in (REDIS, SOURCE_REDIS), 'unknown Redis target')
    require_local_apps_stopped()
    if container == REDIS:
        require(not (STATE / 'target-writes-possible').exists(), 'target may have new writes; refusing Redis reimport')
    c = inspect(container)
    mount = next(m['Source'] for m in c['Mounts'] if m['Destination'] == '/data')
    root = Path(mount)
    require(root.is_dir() and not root.is_symlink(), 'unsafe Redis mount')
    if c['State']['Running']:
        expected = redis_config(container)
    else:
        saved = next((name for name in ('redis-import-started.json', 'redis-import-manifest.json', 'redis-export-manifest.json') if (STATE / name).exists()), None)
        require(saved is not None, 'stopped Redis lacks saved persistence config')
        expected = read(saved)['config']
    save('redis-import-started.json', {'config': expected})
    # Validate the entire stream before touching the previous target contents.
    with tempfile.TemporaryDirectory(prefix='.bwg-dmit-redis-', dir=str(root.parent)) as d:
        incoming = Path(d); data = incoming / 'data'; data.mkdir()
        manifest = None; seen = set()
        with tarfile.open(fileobj=sys.stdin.buffer, mode='r|') as archive:
            for member in archive:
                require(member.isfile() and member.name not in seen, 'unsafe/duplicate Redis archive member')
                seen.add(member.name)
                if member.name == 'migration-manifest.json':
                    require(manifest is None and member.size < 8 * 1024**2, 'invalid Redis manifest')
                    manifest = json.load(archive.extractfile(member))
                    require(manifest.get('format') == 1, 'unsupported Redis stream format')
                    continue
                require(manifest is not None and member.name.startswith('data/'), 'Redis manifest must be first')
                relative = member.name[5:]
                require(relative in manifest['files'] and not Path(relative).is_absolute() and '..' not in Path(relative).parts, 'unexpected Redis archive path')
                target = data / relative; target.parent.mkdir(parents=True, exist_ok=True)
                with target.open('wb') as out: shutil.copyfileobj(archive.extractfile(member), out)
                target.chmod(member.mode & 0o777)
        require(manifest is not None and manifest['config'] == expected, 'Redis persistence config differs')
        require(file_manifest(data) == manifest['files'], 'Redis persistence file hash mismatch or incomplete stream')
        run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'redis-check-rdb', '-v', str(data) + ':/data:ro', c['Image'], '/data/' + expected['dbfilename']])
        aof = expected['appenddirname'] + '/' + expected['appendfilename'] + '.manifest'
        # redis-check-aof opens files O_RDWR even without --fix. This is an
        # isolated incoming copy; verify the checker did not change any byte.
        run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'redis-check-aof', '-v', str(data) + ':/data:rw', c['Image'], '/data/' + aof])
        require(file_manifest(data) == manifest['files'], 'Redis checker changed the incoming persistence files')
        run(['docker', 'stop', '-t', '60', container])
        for child in root.iterdir():
            if child.is_dir() and not child.is_symlink(): shutil.rmtree(child)
            else: child.unlink()
        shutil.copytree(data, root, dirs_exist_ok=True)
        require(file_manifest(root) == manifest['files'], 'Redis installed file hash mismatch')
        save('redis-import-manifest.json', manifest)
        digest = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()
        save('redis-import.json', {'container': container, 'manifest_sha256': digest})
    run(['docker', 'start', container])
    for _ in range(60):
        try:
            if redis_cli(container, 'PING') == 'PONG': break
        except RuntimeError: pass
        time.sleep(1)
    else: raise RuntimeError('target Redis did not become ready after full persistence import')
    print('redis=complete RDB+AOF imported; manifest_sha256=' + digest + '; absolute TTL preserved')


def require_local_apps_stopped():
    stopped((STATE / 'run-id').read_text().strip())


def redis_cli(container, *command):
    args = ['docker', 'exec', container, 'redis-cli', '--no-auth-warning', '--raw'] + list(command)
    p = run(args, check=False)
    # The source container has REDISCLI_AUTH even when the server has no password.
    if b'AUTH failed' in p.stderr:
        p = run(['docker', 'exec', container, 'env', '-u', 'REDISCLI_AUTH', 'redis-cli', '--raw'] + list(command), check=False)
    require(p.returncode == 0 and not p.stderr and not p.stdout.startswith((b'NOAUTH', b'WRONGPASS', b'ERR ', b'(error)', b'LOADING')), 'Redis command/authentication failed')
    return p.stdout.decode().strip()


def redis_config(container):
    config = {}
    for name in ('appendonly', 'dir', 'dbfilename', 'appenddirname', 'appendfilename'):
        lines = redis_cli(container, 'CONFIG', 'GET', name).splitlines()
        require(len(lines) == 2 and lines[0] == name, 'unexpected Redis persistence config')
        config[name] = lines[1]
    require(config['appendonly'] == 'yes' and config['dir'] == '/data', 'review nonstandard Redis persistence before migration')
    for name in ('dbfilename', 'appenddirname', 'appendfilename'):
        require(re.fullmatch(r'[A-Za-z0-9_.-]+', config[name]) and config[name] not in ('.', '..'), 'unsafe Redis persistence name')
    return config


def file_manifest(root):
    result = {}
    for p in sorted(Path(root).rglob('*')):
        require(not p.is_symlink(), 'symlink found in persistence directory')
        if p.is_file(): result[str(p.relative_to(root))] = {'bytes': p.stat().st_size, 'sha256': sha(p)}
    return result


def tree_manifest(root):
    root = Path(root)
    result = {str(p.relative_to(root)): sha(p) for p in sorted(root.rglob('*')) if p.is_file()}
    for db in root.glob('*.db'):
        con = sqlite3.connect('file:' + str(db) + '?mode=ro', uri=True)
        require(con.execute('PRAGMA quick_check').fetchone()[0] == 'ok', 'SQLite quick_check failed')
        con.close()
    print(json.dumps(result, sort_keys=True, indent=2))


def monitor_export():
    # Only the two monitoring sites and their current cert files, never other vhosts.
    with tarfile.open(fileobj=sys.stdout.buffer, mode='w|', dereference=True) as out:
        for domain in DOMAINS:
            out.add('/etc/nginx/sites-available/' + domain, arcname='etc/nginx/sites-available/' + domain)
            for name in ('fullchain.pem', 'privkey.pem'):
                path = '/etc/letsencrypt/live/' + domain + '/' + name
                out.add(path, arcname=path.lstrip('/'))


def monitor_import():
    require(not (STATE / 'monitor-paths.json').exists(), 'monitor config already staged')
    paths = []
    save('monitor-paths.json', paths)
    allowed = {'etc/nginx/sites-available/' + d for d in DOMAINS}
    allowed.update('etc/letsencrypt/live/' + d + '/' + name for d in DOMAINS for name in ('fullchain.pem', 'privkey.pem'))
    with tarfile.open(fileobj=sys.stdin.buffer, mode='r|') as archive:
        for member in archive:
            require(member.isfile() and member.name in allowed, 'unexpected monitoring archive member')
            allowed.remove(member.name)
            path = Path('/') / member.name.replace('etc/letsencrypt/live/', 'etc/nginx/ssl/bwg-dmit-monitor/')
            require(not path.exists(), 'monitor path already exists: ' + str(path))
            data = archive.extractfile(member).read()
            if member.name.startswith('etc/nginx/'):
                data = data.replace(b'/etc/letsencrypt/live/check.', b'/etc/nginx/ssl/bwg-dmit-monitor/check.')
            paths.append(str(path))
            save('monitor-paths.json', paths)
            write(path, data, member.mode & 0o777)
    require(not allowed, 'monitor TLS/vhost archive incomplete')
    save('monitor-paths.json', paths)
    for domain in DOMAINS:
        dest = Path('/etc/nginx/sites-enabled') / domain
        require(not dest.exists(), 'monitor enabled path exists')
        dest.symlink_to(Path('/etc/nginx/sites-available') / domain)
    run(['/usr/sbin/nginx', '-t'])
    run(['systemctl', 'reload', 'nginx'])
    print('monitor-Nginx=ready; active TLS certs copied; DNS unchanged')


def monitor_restore():
    if (STATE / 'monitor-paths.json').exists():
        for domain in DOMAINS:
            (Path('/etc/nginx/sites-enabled') / domain).unlink(missing_ok=True)
        for p in read('monitor-paths.json'):
            Path(p).unlink(missing_ok=True)
        run(['/usr/sbin/nginx', '-t'])
        run(['systemctl', 'reload', 'nginx'])


def data_service(action):
    path = Path('/etc/systemd/system/sub2api-dmit-data.service')
    if action == 'remove':
        if path.exists():
            run(['systemctl', 'disable', '--now', path.name])
            path.unlink(); run(['systemctl', 'daemon-reload'])
        return
    require(not path.exists(), 'data boot service already exists')
    start = shlex.join(stage_compose('up', '-d', 'postgres', 'redis', 'komari'))
    stop = shlex.join(stage_compose('stop'))
    # systemd does not search for argv[0] on all supported distributions.
    start = start.replace(start.split()[0], shutil.which(compose()[0]), 1)
    stop = stop.replace(stop.split()[0], shutil.which(compose()[0]), 1)
    unit = ('[Unit]\nDescription=Sub2API DMIT private data stack\n'
            'After=docker.service wg-quick@wg0.service sub2api-dmit-data-firewall.service\n'
            'Requires=docker.service wg-quick@wg0.service sub2api-dmit-data-firewall.service\n'
            'PartOf=docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\n'
            'TimeoutStartSec=180\nExecStart=' + start + '\nExecStop=' + stop + '\n'
            '[Install]\nWantedBy=multi-user.target\n')
    write(path, unit, 0o644)
    run(['systemctl', 'daemon-reload']); run(['systemctl', 'enable', '--now', path.name])
    print('data-boot-order=WireGuard -> firewall -> data containers')


def writers_check(container):
    rows = sql(container, "SELECT count(*) FROM pg_stat_activity WHERE datname='sub2api' AND pid<>pg_backend_pid() AND backend_type='client backend'")
    require(rows == '0', 'database still has client connections; stop external writers before final snapshot')
    print('database-writers=0')


def source_freeze():
    names = ('sub2api-postgres', 'sub2api-redis', 'sub2api-postgres-relay', 'sub2api-redis-relay', 'komari')
    if not (STATE / 'source-restarts.json').exists():
        save('source-restarts.json', {n: inspect(n)['HostConfig']['RestartPolicy'] for n in names})
    for name in names: run(['docker', 'update', '--restart=no', name])
    run(['docker', 'stop', '-t', '30', 'komari'])
    print('source-Komari=stopped (SQLite frozen); source-Redis=write-frozen by stopped apps')


def redis_info(container):
    print(json.dumps({'dbsize': redis_cli(container, 'DBSIZE'),
                      'keyspace': redis_cli(container, 'INFO', 'keyspace'),
                      'used_memory': redis_cli(container, 'INFO', 'memory').split('used_memory:')[1].splitlines()[0]}, sort_keys=True))


def source_finalize():
    for c in ('sub2api-postgres', 'sub2api-redis', 'sub2api-postgres-relay', 'sub2api-redis-relay', 'komari'):
        run(['docker', 'update', '--restart=no', c])
        run(['docker', 'stop', '-t', '30', c])
    validate('bwg-us-01')


def source_unfreeze():
    if (STATE / 'source-restarts.json').exists():
        for name, policy in read('source-restarts.json').items():
            value = policy['Name']
            if value == 'on-failure' and policy['MaximumRetryCount']:
                value += ':' + str(policy['MaximumRetryCount'])
            run(['docker', 'update', '--restart=' + value, name])
    run(['docker', 'start', 'sub2api-postgres-relay', 'sub2api-redis-relay', 'komari'])


def network_check(expected):
    addr = {'bwg-us-01': '10.20.3.2', 'vmiss-us-01': '10.20.0.2', 'yt-us-01': '10.20.1.2', 'vmiss-us-02': '10.20.2.2'}[expected]
    for _ in range(12):
        if run(['ping', '-c', '1', '-W', '3', '10.20.0.1'], check=False).returncode == 0:
            break
        time.sleep(2)
    else:
        raise RuntimeError('DMIT WireGuard hub unreachable')
    require(addr in text(['ip', '-o', '-4', 'addr', 'show', 'wg0']), 'wrong WireGuard address')
    handshakes = text(['wg', 'show', 'wg0', 'latest-handshakes']).splitlines()
    require(handshakes and all(0 <= time.time() - int(s.split()[1]) < 180 for s in handshakes), 'no fresh handshake')
    print(expected + ': network=ok; fresh-handshake=ok')


def firewall_install():
    path = Path('/usr/local/sbin/sub2api-dmit-data-firewall')
    require(not path.exists(), 'migration firewall already installed')
    original = read('app.inspect.json')
    networks = list(original['NetworkSettings']['Networks'])
    require(len(networks) == 1, 'unexpected multiple app networks')
    subnet = json.loads(text(['docker', 'network', 'inspect', networks[0]]))[0]['IPAM']['Config'][0]['Subnet']
    script = '''#!/bin/sh
set -eu
action=${1:-check}
rule() {
 chain=$1; shift
 case "$action" in
 start) iptables -w 5 -C "$chain" "$@" 2>/dev/null || iptables -w 5 -I "$chain" 1 "$@" ;;
 stop) if iptables -w 5 -C "$chain" "$@" 2>/dev/null; then iptables -w 5 -D "$chain" "$@"; fi ;;
 check) iptables -w 5 -C "$chain" "$@" ;;
 *) exit 64 ;;
 esac
}
for port in 5432 6379 25774; do
 rule DOCKER-USER -p tcp -m conntrack --ctorigdst 10.20.0.1 --ctorigdstport "$port" -m comment --comment sub2api-dmit-data -j DROP
 rule DOCKER-USER -s APP_SUBNET -p tcp -m conntrack --ctorigdst 10.20.0.1 --ctorigdstport "$port" -m comment --comment sub2api-dmit-data -j ACCEPT
 for source in 10.20.3.2 10.20.0.2 10.20.1.2 10.20.2.2; do
  rule DOCKER-USER -i wg0 -s "$source/32" -p tcp -m conntrack --ctorigdst 10.20.0.1 --ctorigdstport "$port" -m comment --comment sub2api-dmit-data -j ACCEPT
 done
 rule DOCKER-USER -p tcp -m conntrack --ctorigdst 10.20.0.1 --ctorigdstport "$port" --ctstate ESTABLISHED,RELATED -m comment --comment sub2api-dmit-data -j ACCEPT
 rule INPUT -i wg0 -d 10.20.0.1 -p tcp --dport "$port" -m comment --comment sub2api-dmit-data -j ACCEPT
done
for source in 95.169.18.157 154.23.243.26; do
 rule INPUT -s "$source/32" -p udp --dport 51820 -m comment --comment sub2api-dmit-data -j ACCEPT
done
'''.replace('APP_SUBNET', subnet)
    write(path, script, 0o700)
    unit = '''[Unit]
Description=Private Sub2API data service access on DMIT
After=docker.service network-online.target
Requires=docker.service
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/sub2api-dmit-data-firewall start
ExecStop=/usr/local/sbin/sub2api-dmit-data-firewall stop
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
'''
    write('/etc/systemd/system/sub2api-dmit-data-firewall.service', unit)
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'enable', '--now', 'sub2api-dmit-data-firewall'])


def firewall_remove():
    path = Path('/usr/local/sbin/sub2api-dmit-data-firewall')
    if path.exists():
        run(['systemctl', 'disable', '--now', 'sub2api-dmit-data-firewall'])
        path.unlink()
        Path('/etc/systemd/system/sub2api-dmit-data-firewall.service').unlink(missing_ok=True)
        run(['systemctl', 'daemon-reload'])


def validate(expected):
    inventory(expected)
    e = env(inspect('sub2api'))
    require(e.get('DATABASE_HOST') == '10.20.0.1' and e.get('REDIS_HOST') == '10.20.0.1', 'app still uses old local data services')
    require(e.get('INSTANCE_ROLE') == ('control' if expected == 'dmit-us-01' else 'gateway'), 'wrong role')
    if expected != 'dmit-us-01':
        network_check(expected)
    run(['/usr/sbin/nginx', '-t'])


def runtime_check():
    import urllib.request
    e = env(inspect('sub2api'))
    token = e.get('GATEWAY_ROUTING_RUNTIME_TOKEN')
    require(token, 'control routing runtime token missing')
    req = urllib.request.Request('http://127.0.0.1:8080/api/v1/gateway-routing/runtime', headers={'X-Gateway-Routing-Token': token})
    with urllib.request.urlopen(req, timeout=15) as response:
        payload = json.load(response)
    require(payload, 'empty routing runtime')
    print('routing-runtime=200; token=present; no routing-setting mutation issued by migration script')


def validate_data():
    wait_health([PG, REDIS])
    for name, port in ((PG, '5432/tcp'), (REDIS, '6379/tcp'), ('komari', '25774/tcp')):
        bindings = inspect(name)['HostConfig']['PortBindings'][port]
        require(all(x['HostIp'] == '10.20.0.1' for x in bindings), 'data service publishes on a public interface')
    run(['/usr/local/sbin/sub2api-dmit-data-firewall', 'check'])
    print('postgres=healthy; redis=healthy; data-listeners=private; firewall=ok')
    print('migrations=' + sql(PG, 'SELECT count(*) FROM schema_migrations'))
    print('recent_usage=' + sql(PG, "SELECT instance_id,count(*) FROM usage_logs WHERE created_at>now()-interval '15 minutes' GROUP BY instance_id ORDER BY 1"))
    payload = json.loads(text(['curl', '-fsS', '--max-time', '15', 'http://10.20.0.1:25774/api/nodes']))
    nodes = payload.get('data', payload)
    if isinstance(nodes, dict):
        nodes = nodes.get('nodes', nodes.get('clients', []))
    require(sorted(n['name'] for n in nodes) == sorted(['bwg-us-01', 'vmiss-us-01', 'yt-us-01', 'vmiss-us-02', 'dmit-us-01']), 'Komari node IDs differ')
    print('Komari=five nodes preserved')


def cert_renew():
    for domain in DOMAINS:
        run(['certbot', 'certonly', '--webroot', '-w', '/var/www/html', '-d', domain,
             '--non-interactive', '--agree-tos', '--register-unsafely-without-email', '--keep-until-expiring'])
        vhost = Path('/etc/nginx/sites-available') / domain
        write(vhost, vhost.read_text().replace('/etc/nginx/ssl/bwg-dmit-monitor/'+domain+'/', '/etc/letsencrypt/live/'+domain+'/'), 0o644)
    run(['/usr/sbin/nginx', '-t']); run(['systemctl', 'reload', 'nginx'])
    hook = Path('/etc/letsencrypt/renewal-hooks/deploy/20-migration-monitor-nginx')
    if not hook.exists():
        write(hook, '#!/bin/sh\nset -eu\n/usr/sbin/nginx -t\nsystemctl reload nginx\n', 0o755)


def komari_import():
    require(not inspect('komari')['State']['Running'], 'stop source Komari before reverse import')
    # Source tree is the previous online state, not a backup directory.
    root = Path('/opt/komari/data')
    for child in root.iterdir():
        if child.is_dir(): shutil.rmtree(child)
        else: child.unlink()
    p = subprocess.run(['tar', '-C', str(root), '-xf', '-'], stdin=sys.stdin.buffer, stderr=subprocess.PIPE)
    require(p.returncode == 0, 'Komari reverse import failed')


def main():
    os.umask(0o077)
    action, *args = sys.argv[1:]
    if action == 'inventory': inventory(*args)
    elif action == 'snapshot': snapshot(*args)
    elif action == 'compose-probe': compose_probe()
    elif action == 'run-id': print((STATE / 'run-id').read_text().strip())
    elif action == 'existing-run': print((STATE / 'run-id').read_text().strip() if (STATE / 'run-id').exists() else '')
    elif action == 'drift-guard': drift_guard(*args)
    elif action == 'mark': mark(*args)
    elif action == 'stopped': stopped(*args)
    elif action == 'stage': stage(*args)
    elif action == 'source-services': source_services()
    elif action == 'stage-start':
        run(stage_compose('up', '-d', 'postgres', 'redis')); wait_health([PG, REDIS]); print('stage=healthy')
    elif action == 'seed-guard':
        current_run(*args); require(not (STATE / 'target-writes-possible').exists(), 'DMIT has accepted writes'); wait_health([PG])
        (STATE / 'seeded').unlink(missing_ok=True)
    elif action in ('dump', 'restore', 'globals-export', 'schema-export'): db_command(action, *args)
    elif action == 'schema-normalize': schema_normalize(*args)
    elif action == 'globals-import': globals_import(*args)
    elif action == 'db-reset': db_reset(*args)
    elif action == 'manifest': manifest(*args)
    elif action == 'stop-app': stop_app(*args)
    elif action == 'public-key': print(text(['wg', 'show', 'wg0', 'public-key']))
    elif action == 'wg-prepare': wg_prepare(*args)
    elif action == 'wg-apply': wg_apply(*args)
    elif action == 'wg-restore': wg_apply(*args, restore=True)
    elif action == 'relay': relay(*args)
    elif action == 'app-start': app_start(*args)
    elif action == 'app-restore': app_start(*args, restore=True)
    elif action == 'promote': promote()
    elif action == 'redis-export': redis_export(*args)
    elif action == 'redis-import': redis_import(*args)
    elif action == 'redis-info': redis_info(*args)
    elif action == 'redis-export-hash': print(hashlib.sha256(json.dumps(read('redis-export-manifest.json'), sort_keys=True).encode()).hexdigest())
    elif action == 'redis-import-hash': print(read('redis-import.json')['manifest_sha256'])
    elif action == 'tree-manifest': tree_manifest(*args)
    elif action == 'monitor-export': monitor_export()
    elif action == 'monitor-import': monitor_import()
    elif action == 'monitor-restore': monitor_restore()
    elif action == 'writers-check': writers_check(*args)
    elif action == 'source-freeze': source_freeze()
    elif action == 'source-finalize': source_finalize()
    elif action == 'source-unfreeze': source_unfreeze()
    elif action == 'source-data-start': run(['docker', 'start', 'sub2api-postgres', 'sub2api-redis'])
    elif action == 'network-check': network_check(*args)
    elif action == 'firewall-install': firewall_install()
    elif action == 'firewall-remove': firewall_remove()
    elif action == 'validate': validate(*args)
    elif action == 'validate-data': validate_data()
    elif action == 'runtime-check': runtime_check()
    elif action == 'cert-renew': cert_renew()
    elif action == 'komari-import': komari_import()
    elif action == 'komari-start': run(stage_compose('up', '-d', 'komari'))
    elif action == 'komari-stop':
        if run(['docker','inspect','komari'],check=False).returncode==0: run(['docker','stop','-t','30','komari'])
    elif action == 'stage-stop': run(stage_compose('stop'))
    elif action == 'data-service': data_service(*args)
    elif action == 'writes-state': print('yes' if (STATE / 'target-writes-possible').exists() else 'no')
    elif action == 'cleanup-state':
        current_run(*args)
        require(STATE == Path('/run/bwg-dmit-migration') and not STATE.is_symlink(), 'unexpected state path')
        shutil.rmtree(STATE)
    elif action == 'cutover-guard':
        current_run(*args)
        require((STATE / 'seeded').exists(), 'seed rehearsal must complete first')
        require(not (STATE / 'target-writes-possible').exists(), 'target may have new writes; use rollback instead')
        wait_health([PG, REDIS])
        require(shutil.which('certbot'), 'certbot missing on DMIT')
        require(not (STATE / 'network-ready').exists(), 'partial cutover detected; run rollback before another cutover')
        require(not Path('/etc/nginx/sites-enabled/check.nideyiyi.com').exists(), 'monitor site already staged; resume/rollback this run explicitly')
    else: raise RuntimeError('unknown remote action')


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        print('ERROR: ' + str(exc), file=sys.stderr)
        sys.exit(1)
