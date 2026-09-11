"""Offline safety checks for the lab driver; no kubectl or devices are used."""
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0

import importlib.util
import copy
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


SPEC = importlib.util.spec_from_file_location(
    "iosxe_gnoi_lab_cycle", Path(__file__).resolve().parents[1] / "iosxe-gnoi-lab-cycle.py")
LAB = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LAB)


def health_outputs(version="17.18.03", applications="No App found"):
    # Representative standalone C9300 IOS XE output; no device access in tests.
    return [
        {"command": "show version", "output": f"""Cisco IOS XE Software, Version {version}
System Serial Number               : TEST-SERIAL
Switch Ports Model              SW Version        SW Image              Mode
------ ----- -----              ----------        ----------            ----
*    1 41    C9300-24P          {version}          CAT9K_IOSXE           BUNDLE
"""},
        {"command": "show install summary", "output": f"""[ Switch 1 ] Installed Package(s) Information:
Type  St   Filename/Version
--------------------------------------------------------------------------------
IMG   C    {version}.0.4112
--------------------------------------------------------------------------------
Auto abort timer: inactive
"""},
        {"command": "show gnxi state detail", "output": """Settings
========
  Secure server: Enabled
  Secure password authentication: Enabled
GNMI
====
  State: Provisioned
GNOI
====
  OS Image service
  ----------------
    Admin state: Enabled
    Oper status: Up
    Supported: Supported

  Factory Reset service
  ---------------------
    Admin state: Enabled
    Oper status: Up
"""},
        {"command": "show platform software status control-processor brief", "output": """Load Average
 Slot  Status  1-Min  5-Min 15-Min
1-RP0 Healthy   0.15   0.19   0.18

Memory (kB)
 Slot  Status    Total     Used (Pct)     Free (Pct) Committed (Pct)
1-RP0 Healthy  7678312  3767112 (49%)  3911200 (51%)   5796152 (75%)

CPU Utilization
 Slot  CPU   User System   Nice   Idle    IRQ   SIRQ IOwait
1-RP0    0   1.10   0.90   0.00  98.00   0.00   0.00   0.00
"""},
        {"command": "show app-hosting list", "output": applications},
    ]


class LabCycleTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.args = SimpleNamespace(
            evidence_dir=str(Path(self.temporary.name) / "evidence"),
            context="offline", namespace="lab", revision="a" * 40,
            image="cvk:offline", upgrade_version="17.18.03", downgrade_version="17.18.02",
            upgrade_url="http://images.invalid/upgrade.bin", upgrade_sha256="b" * 64,
            downgrade_url="http://images.invalid/downgrade.bin", downgrade_sha256="c" * 64)
        self.cycle = LAB.Cycle(self.args)
        self.cycle.note = Mock()

    def test_version_matches_exact_or_dotted_build_only(self):
        self.assertTrue(LAB.matches("17.18.03", "17.18.03"))
        self.assertTrue(LAB.matches("17.18.03.0.4112", "17.18.03"))
        for actual in ("17.18.030", "17.18.03a", "17.18.02", ""):
            self.assertFalse(LAB.matches(actual, "17.18.03"))

    def test_create_timeout_is_never_retried(self):
        manifest = {"kind": "IOSXESoftwareUpgrade", "metadata": {"name": "offline-upgrade"}, "spec": {}}
        self.cycle.kubectl = Mock(side_effect=subprocess.TimeoutExpired("kubectl", 45))
        with self.assertRaises(subprocess.TimeoutExpired):
            self.cycle.submit(manifest)
        self.cycle.kubectl.assert_called_once()
        saved = self.cycle.directory / "offline-upgrade.manifest.json"
        self.assertEqual(json.loads(saved.read_text()), manifest)

    def test_uncertain_create_still_collects_read_only_evidence(self):
        self.cycle.worker_snapshot = Mock(return_value=[])
        self.cycle.verify = Mock()
        self.cycle.submit = Mock(side_effect=subprocess.TimeoutExpired("kubectl", 45))
        self.cycle.kubectl = Mock(return_value="{}")
        with self.assertRaises(subprocess.TimeoutExpired):
            self.cycle.transition("cat9k-1", "upgrade")
        self.cycle.submit.assert_called_once()
        self.assertTrue(self.cycle.kubectl.called, "ambiguous create skipped evidence collection")
        self.assertTrue(any(call.args[:3] == ("get", "xeupgrade", f"{self.cycle.prefix}-cat9k-1-upgrade")
                            for call in self.cycle.kubectl.call_args_list))
        for call in self.cycle.kubectl.call_args_list:
            self.assertIn(call.args[0], {"get", "logs"})

    def test_terminal_failure_stops_without_replaying(self):
        self.cycle.get = Mock(return_value={"status": {"phase": "ValidationFailed", "failureReason": "InstallOutcomeUnknown"}})
        self.cycle.submit = Mock()
        with self.assertRaisesRegex(RuntimeError, "ValidationFailed/InstallOutcomeUnknown"):
            self.cycle.wait("xeupgrade", "offline-upgrade", 10)
        self.cycle.get.assert_called_once()
        self.cycle.submit.assert_not_called()
        saved = json.loads((self.cycle.directory / "offline-upgrade.result.json").read_text())
        self.assertEqual(saved["status"]["failureReason"], "InstallOutcomeUnknown")
        history = (self.cycle.directory / "offline-upgrade.observations.jsonl").read_text().splitlines()
        self.assertEqual(json.loads(history[0])["status"], saved["status"])

    def test_wait_timeout_is_observation_only(self):
        self.cycle.get = Mock()
        self.cycle.submit = Mock()
        with patch.object(LAB.time, "monotonic", side_effect=[100, 111]):
            with self.assertRaisesRegex(RuntimeError, "do not resubmit"):
                self.cycle.wait("xeupgrade", "offline-upgrade", 10)
        self.cycle.get.assert_not_called()
        self.cycle.submit.assert_not_called()

    def test_probe_rejects_error_and_truncated_evidence(self):
        self.cycle.submit = Mock()
        for outputs in ([], [{"output": "partial", "truncated": True}], [{"err": "failed"}]):
            with self.subTest(outputs=outputs):
                self.cycle.wait = Mock(return_value={"status": {"outputs": outputs}})
                with self.assertRaisesRegex(RuntimeError, "health evidence"):
                    self.cycle.probe("cat9k-1", "health", "GNOIOSVerify")

    def test_verify_rejects_unhealthy_required_standby(self):
        for standby in ({"State": "UNAVAILABLE"}, {"State": "READY", "Version": "17.18.02"},
                        {"State": "READY", "Version": "17.18.03", "ActivationFailMessage": "activation failed"}):
            with self.subTest(standby=standby):
                payload = {"Version": "17.18.03", "IndividualSupervisorInstall": True, "Standby": standby}
                self.cycle.probe = Mock(return_value=[{"output": json.dumps(payload)}])
                self.cycle.get = Mock(return_value={"status": {"phase": "Ready", "conditions": [{"type": "Ready", "status": "True"}]}})
                with self.assertRaisesRegex(RuntimeError, "[Ss]tandby|supervisor"):
                    self.cycle.verify("cat9k-1", "upgrade", "17.18.03")
                self.cycle.probe.assert_called_once()

    def test_verify_accepts_ciscodevice_ready_phase_and_saves_sanitized_identity(self):
        payload = {"Version": "17.18.03", "Standby": {"State": "NOT_REPORTED"}}
        self.cycle.probe = Mock(side_effect=[[{"output": json.dumps(payload)}], health_outputs()])
        device = {"metadata": {"name": "cat9k-1", "uid": "device-uid", "annotations": {"secret": "do-not-save"}},
                  "spec": {"address": "198.51.100.100", "driver": "XE", "credentials": {"password": "do-not-save"}},
                  "status": {"phase": "Ready"}}
        node = {"metadata": {"name": "cat9k-1"}, "spec": {"taints": [], "private-field": "do-not-save"},
                "status": {"conditions": [{"type": "Ready", "status": "True"}]}}
        self.cycle.get = Mock(side_effect=[device, node])
        self.cycle.wait_maintenance_clear = Mock()
        self.cycle.verify("cat9k-1", "baseline", "17.18.03")
        self.cycle.wait_maintenance_clear.assert_called_once_with("cat9k-1", "cat9k-1.baseline")
        for kind in ("ciscodevice", "node"):
            saved = (self.cycle.directory / f"cat9k-1.baseline.{kind}.json").read_text()
            self.assertNotIn("do-not-save", saved)
        self.assertEqual(self.cycle.baselines["cat9k-1"]["serial"], "TEST-SERIAL")

    def test_node_ready_is_required_before_acceptance(self):
        self.cycle.probe = Mock(return_value=[{"output": json.dumps({"Version": "17.18.03"})}])
        self.cycle.get = Mock(side_effect=[{"status": {"phase": "Ready"}}, {"status": {}}])
        with self.assertRaisesRegex(RuntimeError, "node/cat9k-1 is not Ready"):
            self.cycle.verify("cat9k-1", "baseline", "17.18.03")
        self.cycle.probe.assert_called_once()

    def test_deadline_covers_independent_control_windows(self):
        self.assertEqual(LAB.observation_timeout({"installTimeoutSeconds": 14400, "rebootTimeoutSeconds": 3600}), 40200)

    def test_worker_binary_requires_full_matching_commit(self):
        LAB.verify_worker_revision(f"cisco-vk v1 (commit={'a' * 40}, built=2026-09-10T12:00:00Z)\n", "a" * 40)
        for commit in ("unknown", "a" * 12, "b" * 40, "a" * 40 + "-dirty"):
            with self.subTest(commit=commit), self.assertRaisesRegex(RuntimeError, "full candidate"):
                LAB.verify_worker_revision(f"cisco-vk v1 (commit={commit}, built=now)\n", "a" * 40)

    def test_worker_identity_uses_requested_image_and_runtime_digest(self):
        deployment = {"spec": {"selector": {"matchLabels": {"device": "cat9k-1"}}}}
        pod = {"metadata": {"name": "worker-1", "uid": "pod-uid", "annotations": {"secret": "do-not-save"}},
               "spec": {"containers": [{"name": "cisco-vk", "image": self.args.image, "env": [{"value": "do-not-save"}]}]},
               "status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}],
                          "containerStatuses": [{"name": "cisco-vk", "image": "docker.io/library/cvk:offline",
                                                 "imageID": "sha256:123", "ready": True, "restartCount": 0}]}}
        self.cycle.get = Mock(return_value=deployment)
        self.cycle.kubectl = Mock(side_effect=[json.dumps({"items": [pod]}),
                                 f"cisco-vk v1 (commit={'a' * 40}, built=now)\n"])
        self.cycle.worker_snapshot("cat9k-1", "baseline")
        saved = (self.cycle.directory / "cat9k-1.baseline.workers.json").read_text()
        self.assertNotIn("do-not-save", saved)
        self.assertIn("pod-uid", saved)
        self.assertIn("sha256:123", saved)
        for field, value in (("imageID", "sha256:456"), ("restartCount", 1)):
            changed = copy.deepcopy(pod)
            changed["status"]["containerStatuses"][0][field] = value
            self.cycle.kubectl = Mock(return_value=json.dumps({"items": [changed]}))
            with self.subTest(field=field), self.assertRaisesRegex(RuntimeError, "changed or restarted"):
                self.cycle.worker_snapshot("cat9k-1", "upgrade-after")

    def test_missing_final_logs_stops_successful_cycle(self):
        self.cycle.worker_snapshot = Mock(return_value=[])
        self.cycle.verify = Mock()
        self.cycle.submit = Mock()
        self.cycle.wait = Mock(return_value={"status": {"runningVersion": "17.18.03",
            "primarySupervisorActivationRequested": True, "conditions": [{"type": "Ready", "status": "True"}]}})
        self.cycle.kubectl = Mock(side_effect=["{}", "{}", RuntimeError("logs unavailable")])
        with self.assertRaisesRegex(RuntimeError, "required evidence collection incomplete"):
            self.cycle.transition("cat9k-1", "upgrade")
        self.cycle.submit.assert_called_once()

    def test_maintenance_observation_does_not_mutate_unrelated_taints(self):
        owned = {"key": "cisco.vk/device-maintenance", "value": "gnoi", "effect": "NoSchedule"}
        unrelated = [{"key": "operator", "value": "reserved", "effect": "NoSchedule"},
                     {"key": owned["key"], "value": "manual", "effect": owned["effect"]},
                     {"key": owned["key"], "value": owned["value"], "effect": "NoExecute"}]
        self.assertFalse(LAB.maintenance_taint({"spec": {"taints": unrelated}}))
        self.assertTrue(LAB.maintenance_taint({"spec": {"taints": unrelated + [owned]}}))
        self.cycle.kubectl = Mock(side_effect=[json.dumps({"spec": {"taints": unrelated + [owned]}}),
                                              json.dumps({"spec": {"taints": unrelated}})])
        with patch.object(LAB.time, "sleep") as sleep:
            self.cycle.wait_maintenance_clear("cat9k-1", "after")
        sleep.assert_called_once()
        for call in self.cycle.kubectl.call_args_list:
            self.assertEqual(call.args[:3], ("get", "node", "cat9k-1"))
            self.assertLessEqual(call.kwargs["timeout"], 5)
        evidence = [json.loads(line) for line in (self.cycle.directory / "after.maintenance.jsonl").read_text().splitlines()]
        self.assertEqual([entry["ownedMaintenanceTaintPresent"] for entry in evidence], [True, False])
        self.assertEqual(evidence[-1]["taints"], unrelated)

    def test_maintenance_clear_timeout_stops_in_forty_five_seconds(self):
        self.cycle.observe_maintenance = Mock(return_value=True)
        with patch.object(LAB.time, "monotonic", side_effect=[0, 0, 0, 45]), patch.object(LAB.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "did not clear within 45 seconds"):
                self.cycle.wait_maintenance_clear("cat9k-1", "after")
        self.cycle.observe_maintenance.assert_called_once_with("cat9k-1", "after", "settling", timeout=5)

    def test_inflight_taint_observation_is_evidence_not_a_dispatch_claim(self):
        self.cycle.get = Mock(side_effect=[
            {"spec": {"deviceRef": {"name": "cat9k-1"}}, "status": {"phase": "Activating"}},
            {"status": {"phase": "Succeeded"}}])
        self.cycle.kubectl = Mock(return_value=json.dumps({"spec": {"taints": [
            {"key": "cisco.vk/device-maintenance", "value": "gnoi", "effect": "NoSchedule"}]}}))
        with patch.object(LAB.time, "sleep"):
            self.cycle.wait("xeupgrade", "upgrade", 30)
        self.assertIn("upgrade", self.cycle.maintenance_observed)
        evidence = json.loads((self.cycle.directory / "upgrade.maintenance.jsonl").read_text())
        self.assertEqual(evidence["phase"], "Activating")
        self.assertTrue(evidence["ownedMaintenanceTaintPresent"])


class PlatformHealthTests(unittest.TestCase):
    def test_known_standalone_output_is_accepted(self):
        result = LAB.platform_health(health_outputs(), "17.18.03")
        self.assertEqual(result, {"serial": "TEST-SERIAL", "model": "C9300-24P",
                                 "committedVersion": "17.18.03.0.4112", "controlProcessor": "1-RP0", "applications": {}})

    def test_unknown_or_unhealthy_platform_evidence_stops(self):
        changes = [
            (0, "17.18.03", "17.18.02"),
            (0, "System Serial Number", "Missing Serial Number"),
            (1, "IMG   C", "IMG   U"),
            (1, "inactive", "active"),
            (1, "IMG   C    17.18.03.0.4112", "IMG   C    17.18.03.0.4112\nSMU U uncommitted.pkg"),
            (2, "Provisioned", "Unprovisioned"),
            (2, "Secure server: Enabled", "Secure server: Disabled"),
            (2, "Secure password authentication: Enabled", "Secure password authentication: Disabled"),
            (2, "Oper status: Up", "Oper status: Down"),
            (3, "Healthy", "Warning"),
            (3, "Memory (kB)", "Unknown memory output"),
            (4, "No App found", "Unrecognized output"),
            (4, "No App found", "App id State\n---\nmy-app STOPPED"),
        ]
        for index, old, new in changes:
            with self.subTest(old=old, new=new):
                outputs = health_outputs()
                outputs[index]["output"] = outputs[index]["output"].replace(old, new)
                with self.assertRaises(RuntimeError):
                    LAB.platform_health(outputs, "17.18.03")

    def test_partial_or_truncated_platform_evidence_stops(self):
        for outputs in (health_outputs()[:-1], health_outputs() + [health_outputs()[0]]):
            with self.assertRaisesRegex(RuntimeError, "incomplete"):
                LAB.platform_health(outputs, "17.18.03")
        outputs = health_outputs()
        outputs[0]["truncated"] = True
        with self.assertRaisesRegex(RuntimeError, "truncated"):
            LAB.platform_health(outputs, "17.18.03")

    def test_baseline_applications_must_survive_upgrade(self):
        baseline = LAB.platform_health(health_outputs("17.18.02", "App id State\n---\nmy-app RUNNING"), "17.18.02")
        LAB.platform_health(health_outputs("17.18.03", "App id State\n---\nmy-app RUNNING"), "17.18.03", baseline)
        with self.assertRaisesRegex(RuntimeError, "changed from baseline"):
            LAB.platform_health(health_outputs(), "17.18.03", baseline)

    def test_stacked_switch_output_is_not_treated_as_standalone(self):
        outputs = health_outputs()
        outputs[0]["output"] += "     2 41    C9300-24P          17.18.03          CAT9K_IOSXE           BUNDLE\n"
        with self.assertRaisesRegex(RuntimeError, "one standalone"):
            LAB.platform_health(outputs, "17.18.03")


if __name__ == "__main__":
    unittest.main()
