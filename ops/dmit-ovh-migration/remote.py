#!/usr/bin/env python3
"""Pinned-SSH helper. Only projected fields and named actions leave the host."""

import json
import os
import re
import subprocess
import sys
from urllib.request import urlopen


SOURCE = ("sub2api-migration-postgres", "sub2api-migration-redis")
TARGET = ("sub2api-ovh-postgres", "sub2api-ovh-redis")
LIVENESS = "http://127.0.0.1:8080/health/ready"


class Stop(RuntimeError):
    pass


def run(*args, timeout=35, ok=(0,)):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    if result.returncode not in ok:
        # Docker/SQL stderr may include credentials or connection details.
        raise Stop(f"{args[0]} {args[1]} failed (exit {result.returncode})")
    return result.stdout.strip()


def container(name):
    raw = run("docker", "inspect", "--type", "container", "--format",
              "{{.Id}} {{.Image}} {{.State.Status}} {{.HostConfig.RestartPolicy.Name}}", name)
    cid, image, status, *policy = raw.split()
    return {"id": cid, "image": image, "status": status, "restart": policy[0] if policy else "no"}


def sql(name, query, read_only=True):
    options = "-c statement_timeout=30000" + (" -c default_transaction_read_only=on" if read_only else "")
    return run("docker", "exec", "-u", "postgres", "-e", f"PGOPTIONS={options}", name, "sh", "-ec",
               'exec psql -XqAt -v ON_ERROR_STOP=1 --no-password -U "$POSTGRES_USER" -d sub2api -c "$1"',
               "sql", query)


def pg_state(name):
    status = container(name)
    if status["status"] != "running":
        return status
    status.update(json.loads(sql(name, """
        SELECT json_build_object(
          'system_id', (pg_control_system()).system_identifier::text,
          'version', current_setting('server_version_num')::int,
          'database', current_database(),
          'database_bytes', pg_database_size(current_database()),
          'recovery', pg_is_in_recovery(),
          'receiving', EXISTS (SELECT 1 FROM pg_stat_wal_receiver WHERE status = 'streaming'),
          'pause_state', CASE WHEN pg_is_in_recovery() THEN pg_get_wal_replay_pause_state() END,
          'lsn', CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn()
                      ELSE pg_current_wal_flush_lsn() END,
          'insert_lsn', CASE WHEN NOT pg_is_in_recovery() THEN pg_current_wal_insert_lsn() END,
          'clients', (SELECT count(*) FROM pg_stat_activity
                      WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()),
          'prepared_xacts', (SELECT count(*) FROM pg_prepared_xacts),
          'wal_slot_limit_mb', current_setting('max_slot_wal_keep_size')
        )
    """)))
    return status


def redis(name, *args):
    result = run("docker", "exec", name, "sh", "-ec", """
        unset REDISCLI_AUTH
        probe=$(redis-cli --raw PING)
        case "$probe" in
          PONG) ;;
          *NOAUTH*) export REDISCLI_AUTH="${REDIS_PASSWORD:?configured Redis credential required}" ;;
          *) exit 3 ;;
        esac
        exec redis-cli -e --raw "$@"
    """, "redis", *args)
    if re.match(r"^(ERR|NOAUTH|WRONGPASS|NOPERM|READONLY|LOADING|BUSY)\b", result):
        raise Stop("Redis rejected the requested operation")
    return result


def info(text):
    return dict(line.split(":", 1) for line in text.splitlines() if ":" in line)


def redis_state(name):
    status = container(name)
    if status["status"] != "running":
        return status
    replication = info(redis(name, "INFO", "replication"))
    persistence = info(redis(name, "INFO", "persistence"))
    server = info(redis(name, "INFO", "server"))
    clients = redis(name, "CLIENT", "LIST", "TYPE", "normal").splitlines()
    # The CLIENT LIST call itself is a normal client, which is excluded.
    status.update({
        "version": server["redis_version"], "role": replication["role"],
        "replication_id": replication["master_replid"],
        "offset": int(replication.get("slave_repl_offset", replication["master_repl_offset"])),
        "link": replication.get("master_link_status"),
        "sync_in_progress": replication.get("master_sync_in_progress") == "1",
        "read_only": replication.get("slave_read_only") == "1",
        "replicas": int(replication["connected_slaves"]),
        "clients": max(len(clients) - 1, 0),
        "aof_ok": persistence.get("aof_enabled") == "1" and persistence.get("aof_last_write_status") == "ok",
        "loading": persistence.get("loading") == "1",
    })
    return status


def data(side):
    pg, cache = SOURCE if side == "source" else TARGET
    return {"postgres": pg_state(pg), "redis": redis_state(cache)}


def ready(url=LIVENESS):
    with urlopen(url, timeout=8) as response:
        result = json.load(response)
    return {k: result.get(k) for k in ("status", "instance_id", "role", "checks")}


def inventory(node):
    app = container("sub2api")
    health = ready()
    if health["instance_id"] != node:
        raise Stop("unexpected Sub2API identity")
    services = {x: run("systemctl", "is-active", x, ok=(0, 3, 4)) for x in ("nginx", "wg-quick@wg0")}
    vfs = os.statvfs("/")
    return {"app": app, "ready": health, "services": services, "root_available_bytes": vfs.f_bavail * vfs.f_frsize,
            "architecture": os.uname().machine, "cpus": os.cpu_count()}


def fence(name, expected):
    actual = container(name)
    if actual["id"] != expected or actual["status"] != "running":
        raise Stop("refusing to fence unexpected or stopped source container")
    run("docker", "update", "--restart=no", expected)
    run("docker", "stop", "--time", "60", expected, timeout=75)
    out = container(name)
    if out["id"] != expected or out["status"] != "exited" or out["restart"] != "no":
        raise Stop("source did not read back stopped with restart disabled")
    return out


def restart(name, expected, policy):
    if policy not in ("no", "always", "unless-stopped"):
        raise Stop("restart policy cannot be restored automatically")
    actual = container(name)
    if actual["id"] != expected or actual["status"] not in ("running", "exited"):
        raise Stop("source container identity or status changed")
    if actual["status"] == "exited":
        run("docker", "start", expected)
    run("docker", "update", "--restart=" + policy, expected)
    return container(name)


def main(args):
    if len(args) == 2 and args[0] == "inventory":
        return inventory(args[1])
    if len(args) == 2 and args[0] == "data" and args[1] in ("source", "target"):
        return data(args[1])
    if args == ["ready"]:
        return ready()
    if args == ["runtime"]:
        raw = run("docker", "exec", "sub2api", "sh", "-ec", """
            test -n "${GATEWAY_ROUTING_RUNTIME_TOKEN:-}"
            wget -qO- -T 8 --header="X-Gateway-Routing-Token: $GATEWAY_ROUTING_RUNTIME_TOKEN" \
              http://127.0.0.1:8080/api/v1/gateway-routing/runtime
        """)
        runtime = json.loads(raw)["data"]
        keys = ("id", "origin", "target_weight", "effective_weight", "max_concurrency", "origin_ready", "auto_disabled", "ovh_geo_effective_percent")
        return {"nodes": [{k: node.get(k) for k in keys} for node in runtime["nodes"]], "monitor_stale": runtime["monitor_stale"]}
    if len(args) == 2 and args[0] == "app" and args[1] in ("sub2api", "sub2api-ovh-control"):
        return container(args[1])
    if args == ["checkpoint"]:
        sql(SOURCE[0], "CHECKPOINT", read_only=False)
        return pg_state(SOURCE[0])
    if len(args) == 3 and args[0] == "fence" and args[1] in SOURCE:
        return fence(args[1], args[2])
    if len(args) == 4 and args[0] == "restart" and args[1] in SOURCE:
        return restart(args[1], args[2], args[3])
    if len(args) == 3 and args[0] == "promote" and args[1] in TARGET:
        name, expected = args[1:]
        if container(name)["id"] != expected:
            raise Stop("target identity changed")
        result = sql(name, "SELECT pg_promote(true, 25)", read_only=False) if name == TARGET[0] else redis(name, "REPLICAOF", "NO", "ONE")
        if result != ("t" if name == TARGET[0] else "OK"):
            raise Stop("target did not report successful promotion")
        return pg_state(name) if name == TARGET[0] else redis_state(name)
    raise Stop("unknown remote action")


if __name__ == "__main__":
    try:
        print(json.dumps(main(sys.argv[1:]), separators=(",", ":")))
    except (Stop, ValueError, KeyError, OSError, subprocess.SubprocessError) as exc:
        print(str(exc) if isinstance(exc, Stop) else f"remote verification failed ({type(exc).__name__})", file=sys.stderr)
        sys.exit(1)
