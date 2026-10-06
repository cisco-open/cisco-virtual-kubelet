"""Negative controls for the offline roadmap acceptance index."""
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0

import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("acceptance", ROOT / "scripts/validate-topology-acceptance.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
CANDIDATE = "a" * 40


class AcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        artifact = self.root / "docs/evidence/result.txt"
        artifact.parent.mkdir(parents=True)
        artifact.write_text("assertions passed; sanitized fixture only\n")
        self.artifact = {"path": "docs/evidence/result.txt",
                         "sha256": hashlib.sha256(artifact.read_bytes()).hexdigest()}
        self.report = {
            "schema_version": 1, "candidate": CANDIDATE, "recorded_at": "2026-10-05T12:00:00Z",
            "gates": [{"id": name, "status": "PASS", "summary": "fixture assertion passed",
                       "next_action": "", "evidence": [self.artifact.copy()], "runs": [{
                           "candidate": CANDIDATE, "command": "fixture-test", "exit_code": 0,
                           "started_at": "2026-10-05T10:00:00Z", "finished_at": "2026-10-05T11:00:00Z",
                           "collector_complete": True, "skipped": 0, "observed": "positive and negative controls passed",
                       }]} for name in sorted(MODULE.REQUIRED)],
        }
        final = next(g for g in self.report["gates"] if g["id"] == "F13")
        final["runs"] = [dict(final["runs"][0], case=case) for case in sorted(MODULE.PHYSICAL_CASES)]

    def check(self, complete=True):
        return MODULE.validate(self.report, self.root, CANDIDATE, complete)

    def test_complete_fixture(self):
        self.assertEqual(len(self.check()), 71)
        final = next(g for g in self.report["gates"] if g["id"] == "F13")
        saved = copy.deepcopy(final["runs"])
        for runs in (saved[:5], saved[:5] + [saved[0]], [saved[0]]):
            final["runs"] = runs
            with self.assertRaisesRegex(MODULE.InvalidEvidence, "six distinct"):
                self.check()

    def test_catalog_matches_documented_acceptance_rows(self):
        plan = (ROOT / "docs/topology-roadmap-execution.md").read_text()
        gates = set(re.findall(r"^\| (E\d{2}-[A-Z]|F\d{2}) \|", plan, re.MULTILINE))
        self.assertEqual(gates, MODULE.REQUIRED)

    def test_checkpoint_does_not_mean_complete(self):
        gate = self.report["gates"][0]
        gate.update(status="BLOCKED", runs=[], evidence=[], next_action="qualify fixture then rerun")
        self.check(complete=False)
        with self.assertRaisesRegex(MODULE.InvalidEvidence, "acceptance incomplete"):
            self.check()

    def test_missing_duplicate_and_unknown_gates_rejected(self):
        original = copy.deepcopy(self.report)
        for change in (lambda g: g.pop(), lambda g: g.append(g[0]),
                       lambda g: g[0].update(id="E99-A")):
            with self.subTest(change=change):
                self.report = copy.deepcopy(original)
                change(self.report["gates"])
                with self.assertRaises(MODULE.InvalidEvidence):
                    self.check()

    def test_incomplete_failed_stale_skipped_and_untyped_runs_rejected(self):
        original = copy.deepcopy(self.report)
        for change in ({"collector_complete": False}, {"exit_code": 1}, {"exit_code": False},
                       {"candidate": "b" * 40}, {"skipped": 1}, {"skipped": -1},
                       {"skipped": False}, {"command": ""}, {"observed": ""},
                       {"started_at": "2026-10-06T00:00:00Z"},
                       {"finished_at": "2026-10-05T13:00:00Z"},
                       {"finished_at": "2026-10-05T11:00:00"}):
            with self.subTest(change=change):
                self.report = copy.deepcopy(original)
                self.report["gates"][0]["runs"][0].update(change)
                with self.assertRaises(MODULE.InvalidEvidence):
                    self.check()

    def test_pass_requires_evidence_and_collector_results(self):
        for field in ("runs", "evidence"):
            original = self.report["gates"][0][field]
            self.report["gates"][0][field] = []
            with self.assertRaises(MODULE.InvalidEvidence):
                self.check()
            self.report["gates"][0][field] = original

    def test_hash_missing_private_and_escaping_artifacts_rejected(self):
        original = copy.deepcopy(self.report)
        outside = self.root / "outside.txt"
        outside.write_text("private")
        (self.root / "docs/evidence/link.txt").symlink_to(outside)
        for change in ({"sha256": "b" * 64}, {"path": "docs/evidence/missing.txt"},
                       {"path": "/tmp/private.txt"}, {"path": "docs/evidence/../../outside.txt"},
                       {"path": "docs/evidence/link.txt"}):
            with self.subTest(change=change):
                self.report = copy.deepcopy(original)
                self.report["gates"][0]["evidence"][0].update(change)
                with self.assertRaises(MODULE.InvalidEvidence):
                    self.check()

    def test_candidate_cannot_be_inferred_from_report(self):
        with self.assertRaisesRegex(MODULE.InvalidEvidence, "independently supplied"):
            MODULE.validate(self.report, self.root, require_complete=True)
        with self.assertRaisesRegex(MODULE.InvalidEvidence, "different candidate"):
            MODULE.validate(self.report, self.root, "b" * 40, True)

    def test_only_measured_cache_decision_can_waive_conditional_gates(self):
        cache = next(g for g in self.report["gates"] if g["id"] == "E09-B")
        cache.update(status="NOT_APPLICABLE", runs=[])
        with self.assertRaises(MODULE.InvalidEvidence):
            self.check()
        self.report["cache_decision"] = {"selected": False, "reason": "measured targets met",
                                         "evidence": [self.artifact.copy()]}
        self.check()
        measurement = next(g for g in self.report["gates"] if g["id"] == "E09-A")
        measurement.update(status="BLOCKED", next_action="measure alternate path")
        with self.assertRaises(MODULE.InvalidEvidence):
            self.check(complete=False)
        measurement["status"] = "PASS"
        self.report["gates"][0]["status"] = "NOT_APPLICABLE"
        with self.assertRaisesRegex(MODULE.InvalidEvidence, "unconditional"):
            self.check()

    def test_no_duplicate_json_keys_or_unknown_fields(self):
        with self.assertRaises(MODULE.InvalidEvidence):
            json.loads('{"status":"BLOCKED","status":"PASS"}', object_pairs_hook=MODULE.unique_object)
        self.report["gates"][0]["skip"] = True
        with self.assertRaises(MODULE.InvalidEvidence):
            self.check()


if __name__ == "__main__":
    unittest.main()
