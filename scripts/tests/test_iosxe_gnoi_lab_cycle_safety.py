#!/usr/bin/env python3
"""Focused safety tests for the physical-lab execution harness."""
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "iosxe-gnoi-lab-cycle.py"
SPEC = importlib.util.spec_from_file_location("iosxe_gnoi_lab_cycle", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class HarnessSafetyTests(unittest.TestCase):
    def test_unsafe_force_delete_is_not_accepted_as_drain_evidence(self):
        with self.assertRaisesRegex(RuntimeError, "unsafe workload drain"):
            MODULE.validate_drain_log("level=error msg=Force deleting pod in running state")
        MODULE.validate_drain_log("pod eviction accepted; replacement Ready")

    def test_vk_force_delete_marker_is_accepted_only_with_exact_clean_proof(self):
        namespace = "cvk-pr194-workloads"
        pod = "app-0"
        uid = "11111111-1111-4111-8111-111111111111"
        benign = "\n".join([
            f"level=info msg=managed drain device-clean completion acknowledged without device mutation key={namespace}/{pod} uid={uid}",
            f"level=info msg=Event(v1.ObjectReference{{Kind:\"Pod\", Namespace:\"{namespace}\", Name:\"{pod}\", UID:\"{uid}\"}}): type: 'Normal' reason: 'ProviderDeleteSuccess'",
            f"level=error msg=Force deleting pod in running state key={namespace}/{pod}/{uid}",
        ])
        MODULE.validate_drain_log(benign)

        missing_event = benign.replace("ProviderDeleteSuccess", "ProviderDeleteFailed")
        with self.assertRaisesRegex(RuntimeError, "lacks exact clean-delete proof"):
            MODULE.validate_drain_log(missing_event)

        lines = benign.splitlines()
        invalid = [
            "\n".join([lines[2], lines[0], lines[1]]),  # proof after deletion
            benign.replace(uid, "22222222-2222-4222-8222-222222222222", 1),
            benign.replace(f'Namespace:"{namespace}"', 'Namespace:"foreign"'),
            benign.replace(f"key={namespace}/{pod} uid", f"key={namespace}/{pod}-other uid"),
            benign.replace(f"uid={uid}", f"uid={uid}0"),
        ]
        for candidate in invalid:
            with self.subTest(log=candidate):
                with self.assertRaisesRegex(RuntimeError, "lacks exact clean-delete proof"):
                    MODULE.validate_drain_log(candidate)
        MODULE.validate_drain_log(benign.replace('"', '\\"'))

    def test_http_source_does_not_gain_a_secret(self):
        image = MODULE.source_manifest("https://images.example/cat9k.bin", "a" * 64)
        self.assertEqual(image["sources"], [{"name": "lab-source", "priority": 10,
                                             "url": "https://images.example/cat9k.bin"}])

    def test_sftp_source_requires_explicit_secret(self):
        with self.assertRaisesRegex(ValueError, "Secret"):
            MODULE.source_manifest("sftp://images.example/cat9k.bin", "a" * 64)
        image = MODULE.source_manifest("sftp://images.example/cat9k.bin", "a" * 64, "image-source")
        self.assertEqual(image["sources"][0]["urlSecretRef"], {"name": "image-source"})

    def test_frozen_plan_is_checked_against_operator_intent(self):
        rollout = {
            "spec": {"plan": {"targetVersion": "17.18.03"}},
            "status": {"frozenPlan": {"hash": "sha256:" + "b" * 64, "targets": [{
                "deviceName": "cat9k-100",
                "source": {"url": "sftp://images.example/cat9k.bin", "sha256": "a" * 64,
                           "secretName": "image-source"},
            }]}}
        }
        MODULE.validate_frozen_plan(rollout, "cat9k-100", "17.18.03",
                                     "sftp://images.example/cat9k.bin", "a" * 64, "image-source")
        with self.assertRaisesRegex(RuntimeError, "source differs"):
            MODULE.validate_frozen_plan(rollout, "cat9k-100", "17.18.03",
                                         "sftp://other.example/cat9k.bin", "a" * 64, "image-source")
        rollout["status"]["frozenPlan"]["targets"].append(rollout["status"]["frozenPlan"]["targets"][0].copy())
        with self.assertRaisesRegex(RuntimeError, "exactly"):
            MODULE.validate_frozen_plan(rollout, "cat9k-100", "17.18.03",
                                         "sftp://images.example/cat9k.bin", "a" * 64, "image-source")


if __name__ == "__main__":
    unittest.main()
