import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import cloudflare as cf
import migrate as m
import remote


def data(recovery=False):
    return {
        "postgres": {"id": "dst-pg" if recovery else "src-pg", "status": "running", "image": "pg-image",
                     "database": "sub2api", "version": 180004, "system_id": "cluster-A", "recovery": recovery,
                     "receiving": recovery, "pause_state": "not paused", "lsn": "0/1000", "insert_lsn": "0/1000",
                     "clients": 0, "prepared_xacts": 0, "restart": "unless-stopped"},
        "redis": {"id": "dst-redis" if recovery else "src-redis", "status": "running", "image": "redis-image",
                  "version": "8.8.1", "role": "slave" if recovery else "master", "read_only": recovery,
                  "link": "up", "offset": 76000, "replicas": 0 if recovery else 1, "clients": 0,
                  "aof_ok": True, "replication_id": "redis-A", "restart": "unless-stopped"},
    }


class ReplicaTests(unittest.TestCase):
    def test_sql_always_selects_business_database(self):
        with patch.object(remote, "run", return_value="ok") as run:
            remote.sql(remote.SOURCE[0], "SELECT current_database()")
        self.assertIn('-d sub2api', run.call_args.args[-3])
        self.assertNotIn('POSTGRES_DB', run.call_args.args[-3])

    def test_frozen_replica_requires_replay_and_offset(self):
        src, dst = data(), data(True)
        self.assertTrue(m.replica_pair(src, dst, frozen=True))
        src["postgres"]["insert_lsn"] = "1/0"
        self.assertFalse(m.replica_pair(src, dst, frozen=True))
        dst["postgres"]["lsn"] = "1/0"
        src["redis"]["offset"] += 1
        self.assertFalse(m.replica_pair(src, dst, frozen=True))
        dst["redis"]["offset"] += 1
        self.assertTrue(m.replica_pair(src, dst, frozen=True))

    def test_reject_wrong_cluster_wrong_link_or_open_writer(self):
        for name, field, value in (("postgres", "system_id", "other"), ("postgres", "database", "postgres"),
                                   ("postgres", "receiving", False), ("postgres", "pause_state", "paused"),
                                   ("redis", "replication_id", "other"), ("redis", "read_only", False),
                                   ("redis", "clients", 1), ("redis", "sync_in_progress", True)):
            with self.subTest(name=name, field=field):
                src, dst = data(), data(True)
                dst[name][field] = value
                with self.assertRaises(m.Stop):
                    m.replica_pair(src, dst, frozen=True)


class FakeMigration(unittest.TestCase):
    def setUp(self):
        directory = self.enterContext(tempfile.TemporaryDirectory())
        self.journal = self.enterContext(m.Journal(directory))
        self.src, self.dst = data(), data(True)
        self.journal.save(run_id="fixture", phase="edge-live", pending=None, source=copy.deepcopy(self.src),
                          target_ids=m.identities(self.dst), target_writes_possible=False)
        self.migration = m.Migration(self.journal, {})
        self.events = []
        self.enterContext(patch.object(m, "ssh", self.ssh))
        self.enterContext(patch.object(self.migration, "hook", self.hook))
        self.fail = None

    def ssh(self, node, action, *args):
        self.events.append((action, node, *args))
        if action == "data":
            return copy.deepcopy(self.src if node == m.SOURCE_NODE else self.dst)
        if action == "app":
            return {"status": "running" if self.fail == "writer" and node == "vmiss-us-01" else "exited"}
        if action == "ready":
            return {"status": "ready", "role": "control", "instance_id": m.TARGET_NODE,
                    "checks": {"database": "ok", "redis": "ok"}}
        if action == "checkpoint":
            return {}
        if action == "fence":
            kind = m.DATA_NAMES[m.SOURCE.index(args[0])]
            self.src[kind].update(status="exited", restart="no")
            if self.fail == "fence-timeout":
                raise m.Stop("fence result unknown")
        if action == "promote":
            saved = json.loads(self.journal.path.read_text())
            assert saved["target_writes_possible"] and saved["pending"] in ("cutover-data", "resume-after-promote")
            if args[0] == m.TARGET[0]:
                self.dst["postgres"]["recovery"] = False
            else:
                self.dst["redis"]["role"] = "master"
            if self.fail == "promote-timeout":
                raise m.Stop("promotion result unknown")
        if action == "restart":
            self.src[m.DATA_NAMES[m.SOURCE.index(args[0])]].update(status="running", restart=args[2])
        return {}

    def hook(self, action, *args):
        self.events.append(("hook", action, *args))
        return {"ok": True}

    def test_cutover_fences_both_before_promotion_and_reopens_after_verification(self):
        self.migration.cutover()
        phases = [event[:2] for event in self.events]
        fence = [i for i, (action, _) in enumerate(phases) if action == "fence"]
        promote = [i for i, (action, _) in enumerate(phases) if action == "promote"]
        self.assertEqual((len(fence), len(promote)), (2, 2))
        self.assertLess(max(fence), min(promote))
        self.assertLess(phases.index(("hook", "verify-data")), phases.index(("hook", "resume")))
        self.assertEqual(self.journal.state["phase"], "ovh-data-live")

    def test_uncertain_promotion_never_restarts_old_primary(self):
        self.fail = "promote-timeout"
        with self.assertRaisesRegex(m.Stop, "promotion result unknown"):
            self.migration.cutover()
        with self.assertRaisesRegex(m.Stop, "automatic old-data rollback prohibited"):
            self.migration.rollback_before_promote()
        self.assertFalse(any(event[0] == "restart" for event in self.events))
        self.assertTrue(self.journal.state["target_writes_possible"])

    def test_resume_after_partial_promotion_only_promotes_remaining_store(self):
        self.fail = "promote-timeout"
        with self.assertRaisesRegex(m.Stop, "promotion result unknown"):
            self.migration.cutover()
        self.fail = None
        self.migration.resume_after_promote()
        all_promotes = [event[2] for event in self.events if event[0] == "promote"]
        self.assertEqual(all_promotes, list(m.TARGET))
        self.assertTrue(m.primary(self.dst))
        self.assertFalse(any(event[0] == "restart" for event in self.events))

    def test_before_promotion_partial_fence_can_be_rolled_back_after_readback(self):
        self.fail = "fence-timeout"
        with self.assertRaisesRegex(m.Stop, "fence result unknown"):
            self.migration.cutover()
        self.assertFalse(self.journal.state["target_writes_possible"])
        self.fail = None
        self.migration.rollback_before_promote()
        self.assertTrue(m.primary(self.src))
        self.assertIsNone(self.journal.state["pending"])
        self.assertEqual(self.journal.state["phase"], "edge-live")

    def test_running_writer_never_reaches_fence(self):
        self.fail = "writer"
        with self.assertRaisesRegex(m.Stop, "not stopped/drained"):
            self.migration.cutover()
        self.assertFalse(any(event[0] in ("fence", "promote") for event in self.events))

    def test_interrupted_phase_and_lock_cannot_be_bypassed(self):
        self.journal.save(pending="publish-edge")
        with self.assertRaisesRegex(m.Stop, "interrupted phase"):
            self.migration.cutover()
        with self.assertRaisesRegex(m.Stop, "state lock"):
            m.Journal(str(self.journal.directory))
        self.assertEqual(self.events, [])


class FakeEdge(cf.Edge):
    def __init__(self):
        self.zone = "/zone"
        self.records = {p: {"id": str(i), "pattern": p, "script": cf.WORKER} for i, p in enumerate(cf.LEGACY)}
        self.mutations = []

    def api(self, method, path, payload=None):
        if method == "GET":
            return list(self.records.values())
        self.mutations.append((method, path, payload))
        if method == "POST":
            self.records[payload["pattern"]] = {"id": "new-" + str(len(self.mutations)), **payload}
        elif method == "DELETE":
            self.records = {p: row for p, row in self.records.items() if row["id"] != path.rsplit("/", 1)[-1]}


class EdgeTests(unittest.TestCase):
    def test_add_new_routes_before_removing_old_and_restore(self):
        edge = FakeEdge()
        edge.set_routes({cf.ROUTE})
        self.assertEqual(edge.mutations[0][0], "POST")
        self.assertEqual(edge.routes().keys(), {cf.ROUTE})
        edge.set_routes(cf.LEGACY)
        self.assertEqual(edge.routes().keys(), cf.LEGACY)

    def test_overlapping_rule_blocks_all_mutations(self):
        for pattern in ("xiaohondou.com/admin/*", "*.xiaohondou.com/*", "*xiaohondou.com/*"):
            edge = FakeEdge()
            edge.records[pattern] = {"id": "unrelated", "pattern": pattern, "script": "other"}
            with self.assertRaisesRegex(cf.Stop, "overlaps"):
                edge.set_routes({cf.ROUTE})
            self.assertEqual(edge.mutations, [])

    def test_confirmation_stops_before_network_or_state(self):
        with tempfile.TemporaryDirectory() as home:
            location = Path(home) / "no-state-yet"
            for action in m.WRITES:
                with self.assertRaisesRegex(m.Stop, "no changes made"):
                    m.main([action, "--state", str(location), "--execute", "--confirm", "different"])
            self.assertFalse(location.exists())


if __name__ == "__main__":
    unittest.main()
