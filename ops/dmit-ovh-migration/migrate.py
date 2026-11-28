#!/usr/bin/env python3
"""Guarded coordinator for the xiaohondou.com topology migration."""

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import tempfile
import time
import uuid

import cloudflare as cf
from remote import SOURCE, TARGET


HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
NODES = {
    "dmit-us-01": "dmit-us-01",
    "vmiss-us-01": "root@38.47.117.85",
    "vmiss-us-02": "root@38.47.113.166",
    "ovh-us-01": "debian@147.135.37.51",
}
SOURCE_NODE, TARGET_NODE = "dmit-us-01", "ovh-us-01"
ORIGINS = dict(zip(("vmiss-us-01", "vmiss-us-02", SOURCE_NODE, TARGET_NODE), cf.ORIGIN_HOSTS))
DATA_NAMES = ("postgres", "redis")
NOMINAL_ROLES = dict.fromkeys(NODES, "gateway") | {SOURCE_NODE: "control"}
WRITES = ("stage", "switch-origins", "publish-edge", "cutover-data", "resume-after-promote",
          "rollback-edge", "rollback-before-promote", "finalize")
PLAN = """DMIT -> OVH / xiaohondou.com whole-site cutover (no changes in plan)
  preflight       read four nodes, DMIT data/routing; --check-edge also reads Cloudflare
  stage           run reviewed staging profile; prove linked, read-only OVH replicas
  switch-origins  switch the only control to OVH, leave DMIT the data primary; check all origins
  publish-edge    activate pinned Worker version; change two DNS A records and site route
  cutover-data    maintenance, drain, compare WAL/Redis, fence DMIT, promote OVH, verify, reopen
  resume-after-promote      verify DMIT is still fenced, then finish an interrupted OVH promotion
  verify          read edge, runtime, origins, and primary/fencing state
  rollback-edge   restore legacy Worker version and routes without changing data authority
  rollback-before-promote   restore DMIT only if target promotion was never attempted
  finalize        read back acceptance and use reviewed cleanup; preserve old data and unrelated services
Writes require a private profile, stable run state, and --execute --confirm <action>.
"""


class Stop(RuntimeError):
    pass


def require(condition, why):
    if not condition:
        raise Stop(why)


def local(args, *, input_text=None, timeout=110, env=None):
    p = subprocess.run(args, input=input_text, capture_output=True, text=True, timeout=timeout, env=env,
                       stdin=subprocess.DEVNULL if input_text is None else None, check=False)
    if p.returncode:
        # Never log subprocess output, which can contain environment or data.
        raise Stop(f"{Path(args[0]).name} failed (exit {p.returncode})")
    return p.stdout.strip()


def ssh(node, *args):
    command = shlex.join([*([] if node != TARGET_NODE else ["sudo", "-n"]), "python3", "-", *args])
    if node == TARGET_NODE:
        # The exact OVH identity and its pinned host key are resolved by Vaultwarden.
        helper = Path(os.environ.get("CODEX_SKILLS_ROOT", "") or local([
            "plutil", "-extract", "skills", "raw", "-o", "-",
            str(Path.home() / "Library/Application Support/CDD Sync/paths.plist"),
        ])) / "pwd-xiaofanqie-login/scripts/ssh-login.mjs"
        connection = ["node", str(helper), "run", f"{NODES[node]}:22"]
    else:
        connection = ["ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no",
                      "-o", "IgnoreUnknown=UseKeychain", "-o", "UseKeychain=no", "-o", "PasswordAuthentication=no",
                      "-o", "KbdInteractiveAuthentication=no", "-o", "ConnectTimeout=8", NODES[node]]
    try:
        return json.loads(local([*connection, command], input_text=(HERE / "remote.py").read_text(), timeout=115))
    except (Stop, ValueError, subprocess.SubprocessError) as exc:
        raise Stop(f"{node} {args[0]}: {exc if isinstance(exc, Stop) else type(exc).__name__}") from None


def lsn(text):
    require(isinstance(text, str) and re.fullmatch(r"[A-Fa-f0-9]{1,8}/[A-Fa-f0-9]{1,8}", text), "invalid PostgreSQL LSN")
    first, second = text.split("/")
    return (int(first, 16) << 32) | int(second, 16)


def identities(actual):
    return {name: actual[name]["id"] for name in DATA_NAMES}


def primary(data):
    return (all(data[k]["status"] == "running" for k in DATA_NAMES) and
            data["postgres"].get("database") == "sub2api" and data["postgres"].get("recovery") is False and data["redis"].get("role") == "master" and
            data["redis"].get("aof_ok") is True and not data["redis"].get("loading"))


def replica_pair(source, target, *, frozen=False):
    pg, replica = source["postgres"], target["postgres"]
    r, rr = source["redis"], target["redis"]
    require(primary(source), "DMIT is not the sole healthy data primary")
    require(pg["system_id"] == replica.get("system_id") and pg["version"] == replica.get("version") and
            pg["image"] == replica["image"] and replica.get("database") == "sub2api" and
            replica.get("recovery") is True and replica.get("receiving") is True and
            replica.get("pause_state") == "not paused", "OVH PostgreSQL is not streaming the expected DMIT cluster/image")
    require(r["version"] == rr.get("version") and r["image"] == rr["image"] and rr.get("role") == "slave" and
            rr.get("read_only") is True and rr.get("link") == "up" and not rr.get("sync_in_progress") and
            rr.get("aof_ok") is True and not rr.get("loading") and r["replicas"] == 1 and
            r["replication_id"] == rr.get("replication_id"), "OVH Redis does not match the expected read-only replica")
    if frozen:
        require(pg["clients"] == 0 and pg["prepared_xacts"] == 0 and r["clients"] == 0 and
                replica["clients"] == 0 and rr["clients"] == 0, "data clients or prepared transactions remain")
        return lsn(replica["lsn"]) >= max(lsn(pg["lsn"]), lsn(pg["insert_lsn"])) and rr["offset"] >= r["offset"]
    return True


def verify_runtime(node):
    payload = ssh(node, "runtime")
    rows = {row["id"]: row for row in payload["nodes"]}
    require(len(payload["nodes"]) == len(rows) and set(rows) == set(NODES) and all(
        rows[n]["origin"] == "https://" + ORIGINS[n] for n in rows), "unexpected runtime origin inventory")
    require(not payload["monitor_stale"], "runtime monitoring is stale; check Komari/links")
    require(any(rows[n]["effective_weight"] > 0 and rows[n]["origin_ready"] is True and
                not rows[n]["auto_disabled"] for n in rows if n != TARGET_NODE), "ordinary pool has no ready origin")
    return payload


def inventory(check_edge=False):
    nodes = {n: ssh(n, "inventory", n) for n in NODES}
    for name, result in nodes.items():
        ready = result["ready"]
        require(result["architecture"] in ("x86_64", "amd64") and ready == {
            "status": "ready", "instance_id": name, "role": NOMINAL_ROLES[name],
            "checks": {"database": "ok", "redis": "ok"},
        }, f"{name}: expected live pre-migration role, version architecture and dependencies")
        require(result["services"]["nginx"] == "active", f"{name}: nginx is not active")
    source = ssh(SOURCE_NODE, "data", "source")
    require(primary(source), "DMIT data authority check failed")
    require(all(source[k]["restart"] in ("no", "always", "unless-stopped") for k in DATA_NAMES),
            "source restart policy is not handled by rollback")
    require(nodes[TARGET_NODE]["root_available_bytes"] >= 3 * source["postgres"]["database_bytes"], "OVH free space below migration reserve")
    result = {"nodes": nodes, "data": source, "runtime": verify_runtime(SOURCE_NODE)}
    if check_edge:
        state = cf.Edge().snapshot()
        require(state["routes"].keys() == cf.LEGACY, "existing Cloudflare legacy routes do not match the baseline")
        result["edge"] = state
    return result


def file_hash(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def reviewed_file(raw):
    path = Path(raw).expanduser()
    require(path.is_absolute() and not path.is_symlink(), "profile and hook must be absolute files, not symlinks")
    path = path.resolve(strict=True)
    st = path.stat()
    require(stat.S_ISREG(st.st_mode) and st.st_uid == os.getuid() and not st.st_mode & 0o022,
            "profile/hook must be owned by the operator and not group/world-writable")
    require(not path.is_relative_to(REPO), "profile/hook must be kept outside the source repository")
    return path


def profile(path):
    require(path, "--profile is required for this phase")
    path = reviewed_file(path)
    options = json.loads(path.read_text())
    require(set(options) == {"schema", "hook", "release_commit"} and options["schema"] == 1, "invalid reviewed profile")
    require(re.fullmatch(r"[0-9a-f]{40}", options["release_commit"]), "profile requires the reviewed full main commit SHA")
    hook = reviewed_file(options["hook"])
    require(os.access(hook, os.X_OK), "approved hook is not executable")
    return {"hook": str(hook), "sha": options["release_commit"], "hash": file_hash(path), "hook_hash": file_hash(hook)}


def canonical(sha):
    clean = local(["git", "-C", str(REPO), "status", "--porcelain=v1"])
    require(not clean, "migration checkout must be clean")
    main = local(["git", "-C", str(REPO), "rev-parse", "refs/heads/main"])
    require(main == sha, "reviewed release commit is not local main HEAD")
    remote = local(["git", "-C", str(REPO), "ls-remote", "--exit-code", "--heads", "cdy", "main"])
    require(remote.split() == [sha, "refs/heads/main"], "reviewed release commit is not cdy/main HEAD")
    current = local(["git", "-C", str(REPO), "rev-parse", "HEAD"])
    require(current == sha, "migration tooling must be executed from the reviewed main commit")


class Journal:
    def __init__(self, path):
        require(path, "--state is required")
        raw = Path(path).expanduser()
        require(raw.is_absolute() and not raw.is_symlink(), "state directory must use an absolute non-symlink path")
        self.directory = raw.resolve()
        require(not self.directory.is_relative_to(REPO), "state must be outside the repository")
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        st = self.directory.stat()
        require(st.st_uid == os.getuid() and not st.st_mode & 0o077, "state directory must be private to the operator")
        self.path = self.directory / "journal.json"
        self.lock = os.fdopen(os.open(self.directory / ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600), "w")
        try:
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            self.lock.close()
            raise Stop("another migration command holds the state lock") from None
        self.state = json.loads(self.path.read_text()) if self.path.exists() else {}

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.lock.close()

    def save(self, **fields):
        self.state.update(fields)
        fd, name = tempfile.mkstemp(prefix=".journal-", dir=self.directory)
        try:
            with os.fdopen(fd, "w") as file:
                json.dump(self.state, file, indent=2, sort_keys=True)
                file.write("\n")
                file.flush()
                os.fsync(file.fileno())
            os.replace(name, self.path)
            directory = os.open(self.directory, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if os.path.exists(name):
                os.unlink(name)


class Migration:
    def __init__(self, journal, options):
        self.j = journal
        self.options = options
        self.s = journal.state

    def hook(self, action, *args):
        require(self.options and file_hash(self.options["hook"]) == self.options["hook_hash"], "reviewed hook changed")
        env = {k: v for k, v in os.environ.items() if k not in ("CLOUDFLARE_API_TOKEN", "ROUTING_CONFIG_TOKEN", "ORIGIN_AUTH_TOKEN")}
        env.update(SUB2API_MIGRATION_RUN_ID=self.s["run_id"], SUB2API_MIGRATION_COMMIT=self.options["sha"],
                   SUB2API_MIGRATION_STATE=str(self.j.directory))
        try:
            result = json.loads(local([self.options["hook"], action, *args], timeout=3600, env=env))
        except (Stop, ValueError, subprocess.SubprocessError):
            raise Stop(f"reviewed hook {action!r} did not return a valid receipt; determine live state before retrying") from None
        require(result.get("ok") is True and result.get("phase") == action and result.get("run_id") == self.s["run_id"],
                f"invalid {action} hook receipt")
        return result

    def start(self, phase, prev, *, recover=()):
        require(self.s.get("phase") in prev, f"{phase}: wrong phase ({self.s.get('phase', 'uninitialized')})")
        require(self.s.get("pending") in (None, *recover), f"interrupted phase {self.s.get('pending')}; verify live state before recovery")
        self.j.save(pending=phase)

    def finish(self, phase, **extra):
        self.j.save(phase=phase, pending=None, **extra)

    def target(self):
        target = ssh(TARGET_NODE, "data", "target")
        require(identities(target) == self.s["target_ids"], "OVH data container identity changed")
        return target

    def source(self):
        source = ssh(SOURCE_NODE, "data", "source")
        require(identities(source) == identities(self.s["source"]), "DMIT data container identity changed")
        return source

    def control(self):
        ready = ssh(TARGET_NODE, "ready")
        require(ready == {"status": "ready", "instance_id": TARGET_NODE, "role": "control",
                          "checks": {"database": "ok", "redis": "ok"}}, "OVH control/database/Redis not ready")

    def stage(self):
        require(not self.s or (self.s.get("phase") == "baseline" and self.s.get("pending") == "stage"),
                "stage already finished or wrong run; use this run's next phase")
        if not self.s:
            baseline = inventory(check_edge=True)
            self.j.save(run_id=uuid.uuid4().hex, phase="baseline", pending=None, profile=self.options,
                        source=baseline["data"], edge=baseline["edge"], target_writes_possible=False)
        self.start("stage", ("baseline",), recover=("stage",))
        receipt = self.hook("stage")
        version = receipt.get("worker_version", "")
        edge = cf.Edge()
        require(edge.snapshot() == self.s["edge"], "stage changed active Cloudflare routes, DNS or deployment")
        edge.check_candidate(version)
        target = ssh(TARGET_NODE, "data", "target")
        replica_pair(self.source(), target)
        self.finish("staged", target_ids=identities(target), worker_version=version)

    def switch_origins(self):
        replica_pair(self.source(), self.target())
        self.start("switch-origins", ("staged",), recover=("switch-origins",))
        self.hook("switch-origins")
        self.control()
        for node in NODES.keys() - {TARGET_NODE}:
            require(ssh(node, "app", "sub2api")["status"] == "exited", f"{node}: legacy app did not stop")
        verify_runtime(TARGET_NODE)
        self.hook("verify-origins")
        self.finish("origins-ready")

    def publish_edge(self):
        prior = self.s.get("phase")
        if prior == "ovh-data-live":
            require(self.s.get("edge_rolled_back"), "the whole-site edge is already active")
            self.fenced()
            require(primary(self.target()), "OVH is not the sole verified data primary")
        self.start("publish-edge", ("origins-ready", "ovh-data-live"), recover=("publish-edge",))
        self.control()
        verify_runtime(TARGET_NODE)
        self.hook("verify-origins")
        edge = cf.Edge()
        edge.zone_check()
        edge.check_candidate(self.s["worker_version"])
        base = self.s["edge"]
        require(edge.active_version() in (base["version"], self.s["worker_version"]), "unexpected Worker deployment")
        edge.routes()
        edge.check_dns_ownership(base["dns"])
        edge.move_dns(base["dns"])
        self.hook("verify-dns-origins")
        edge.deploy(base["version"], self.s["worker_version"])
        edge.set_routes({cf.ROUTE})
        self.hook("verify-public")
        self.finish("ovh-data-live" if prior == "ovh-data-live" else "edge-live", edge_rolled_back=False)

    def cutover(self):
        self.start("cutover-data", ("edge-live",))
        self.hook("freeze")
        for node in NODES:
            require(ssh(node, "app", "sub2api")["status"] == "exited", f"{node}: application not stopped/drained")
        source = self.source()
        replica_pair(source, self.target(), frozen=True)
        ssh(SOURCE_NODE, "checkpoint")
        limit = time.monotonic() + 120
        while True:
            source = self.source()
            if replica_pair(source, self.target(), frozen=True):
                break
            require(time.monotonic() < limit, "OVH replicas did not catch up within the allowed window")
            time.sleep(2)
        self.j.save(frozen={"pg_lsn": source["postgres"]["lsn"], "redis_offset": source["redis"]["offset"],
                            "redis_id": source["redis"]["replication_id"]})
        # Once both writers are fenced, still verify the replica copies retained the frozen position.
        for name, kind in zip(SOURCE, DATA_NAMES):
            ssh(SOURCE_NODE, "fence", name, source[kind]["id"])
        self.fenced()
        target = self.target()
        require(target["postgres"].get("recovery") is True and target["redis"].get("role") == "slave" and
                lsn(target["postgres"]["lsn"]) >= lsn(self.s["frozen"]["pg_lsn"]) and
                target["redis"]["replication_id"] == self.s["frozen"]["redis_id"] and
                target["redis"]["offset"] >= self.s["frozen"]["redis_offset"], "OVH frozen replay position no longer matches")
        # Save BEFORE the first promotion attempt, including timeout/partial-success cases.
        self.j.save(target_writes_possible=True)
        self.finish_promotion()

    def resume_after_promote(self):
        require(self.s.get("pending") in ("cutover-data", "resume-after-promote") and self.s.get("target_writes_possible") is True,
                "there is no interrupted promotion to resume")
        self.start("resume-after-promote", ("edge-live",), recover=("cutover-data", "resume-after-promote"))
        self.hook("verify-maintenance")
        self.finish_promotion()

    def finish_promotion(self):
        self.fenced()
        target = self.target()
        pg, redis = target["postgres"], target["redis"]
        original = self.s["source"]
        require(all(target[k]["status"] == "running" and target[k]["image"] == original[k]["image"] for k in DATA_NAMES) and
                pg.get("database") == "sub2api" and pg.get("system_id") == original["postgres"]["system_id"] and
                redis.get("aof_ok") is True, "OVH data store identity or persistence check failed")
        if pg.get("recovery") is True:
            require(lsn(pg["lsn"]) >= lsn(self.s["frozen"]["pg_lsn"]), "OVH PostgreSQL replay position regressed")
            ssh(TARGET_NODE, "promote", TARGET[0], pg["id"])
        if redis.get("role") != "master":
            require(redis.get("role") == "slave" and redis.get("read_only") is True and
                    redis.get("replication_id") == self.s["frozen"]["redis_id"] and
                    redis["offset"] >= self.s["frozen"]["redis_offset"], "OVH Redis is not at the frozen replication point")
            ssh(TARGET_NODE, "promote", TARGET[1], redis["id"])
        require(primary(self.target()), "OVH was not verified as the new writable data authority")
        self.hook("attach-local-data")
        self.control()
        self.hook("verify-data")
        self.hook("resume")
        self.hook("verify-public")
        self.finish("ovh-data-live")

    def fenced(self):
        require(all(row["status"] == "exited" and row["restart"] == "no" for row in self.source().values()),
                "DMIT data is not fenced")

    def rollback_before_promote(self):
        require(self.s.get("pending") in ("cutover-data", "rollback-before-promote"), "nothing to roll back before promotion")
        require(self.s.get("target_writes_possible") is False, "OVH may have written; automatic old-data rollback prohibited")
        self.start("rollback-before-promote", ("edge-live",), recover=("cutover-data", "rollback-before-promote"))
        t = self.target()
        require(t["postgres"].get("recovery") is True and t["redis"].get("role") == "slave" and t["redis"].get("read_only") is True,
                "target read-only state not proven; refusing to start old data")
        self.hook("verify-maintenance")
        for kind, name in zip(DATA_NAMES, SOURCE):
            original = self.s["source"][kind]
            ssh(SOURCE_NODE, "restart", name, original["id"], original["restart"])
        require(primary(self.source()), "DMIT failed to return as primary")
        self.hook("attach-source-data")
        self.control()
        self.hook("resume")
        self.hook("verify-public")
        self.finish("edge-live", frozen=None)

    def rollback_edge(self):
        if self.s.get("phase") == "origins-ready":
            require(self.s.get("pending") in ("publish-edge", "rollback-edge"), "publication was not attempted")
        old_phase = self.s.get("phase")
        self.start("rollback-edge", ("origins-ready", "edge-live", "ovh-data-live"), recover=("publish-edge", "rollback-edge"))
        self.control()
        self.hook("verify-legacy-origins")
        edge = cf.Edge()
        edge.zone_check()
        edge.check_dns_ownership(self.s["edge"]["dns"])
        require(edge.active_version() in (self.s["edge"]["version"], self.s["worker_version"]),
                "unrecognized Worker version; no rollback attempted")
        edge.set_routes(cf.LEGACY)
        edge.deploy(self.s["worker_version"], self.s["edge"]["version"])
        self.hook("verify-legacy-public")
        self.finish("ovh-data-live" if old_phase == "ovh-data-live" else "origins-ready", edge_rolled_back=True)

    def verify(self, *, public_check=True):
        require(self.s.get("pending") is None, "migration interrupted; read status before verification")
        require(self.s["phase"] in ("edge-live", "ovh-data-live", "finalized"), "whole-site publication is not complete")
        self.control()
        runtime = verify_runtime(TARGET_NODE)
        edge = cf.Edge().snapshot()
        require(edge["version"] == self.s["worker_version"] and edge["routes"].keys() == {cf.ROUTE} and
                all(record["content"] == cf.OVH for record in edge["dns"].values()), "whole-site edge readback does not match")
        if self.s.get("target_writes_possible"):
            self.fenced()
            require(primary(self.target()), "OVH data is not writable and healthy")
        else:
            replica_pair(self.source(), self.target())
        if public_check:
            self.hook("verify-public")
        return {"phase": self.s["phase"], "edge": edge, "runtime": runtime}

    def finalize(self):
        require(self.s.get("phase") == "ovh-data-live", "data cutover not completed")
        self.verify()
        self.start("finalize", ("ovh-data-live",))
        self.hook("finalize")
        self.fenced()
        self.finish("finalized")


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("action", nargs="?", default="plan", choices=("plan", "preflight", "status", "verify", *WRITES))
    p.add_argument("--check-edge", action="store_true", help="include authenticated Cloudflare readback in preflight")
    p.add_argument("--profile", default=os.environ.get("SUB2API_MIGRATION_PROFILE"))
    p.add_argument("--state", default=os.environ.get("SUB2API_MIGRATION_STATE"))
    p.add_argument("--execute", action="store_true")
    p.add_argument("--confirm", help="repeat the exact writing phase; user production authorization is still required")
    args = p.parse_args(argv)
    if args.action in WRITES:
        require(args.execute and args.confirm == args.action, f"no changes made; requires --execute --confirm {args.action}")
    else:
        require(not args.execute and not args.confirm, "read-only commands do not accept write flags")
    if args.action == "plan":
        print(PLAN, end="")
        return
    if args.action == "preflight":
        print(json.dumps(inventory(args.check_edge), indent=2))
        return
    with Journal(args.state) as journal:
        if args.action == "status":
            result = {key: journal.state.get(key) for key in ("run_id", "phase", "pending", "target_writes_possible", "edge_rolled_back", "frozen")}
        else:
            options = profile(args.profile)
            require(not journal.state or options == journal.state.get("profile"), "profile or hook differs from the recorded run")
            migration = Migration(journal, options)
            if args.action == "verify":
                result = migration.verify(public_check=False)
            else:
                canonical(options["sha"])
                method = "cutover" if args.action == "cutover-data" else args.action.replace("-", "_")
                getattr(migration, method)()
                result = {"run_id": journal.state["run_id"], "phase": journal.state["phase"], "status": "ok"}
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    os.umask(0o077)
    try:
        main()
    except (Stop, cf.Stop, OSError, KeyError, ValueError, subprocess.SubprocessError) as exc:
        print(f"STOP: {exc if isinstance(exc, (Stop, cf.Stop)) else type(exc).__name__}", file=sys.stderr)
        sys.exit(1)
