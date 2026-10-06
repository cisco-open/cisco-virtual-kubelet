# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0

"""Offline preflight tests: no Kubernetes or Docker writes are permitted."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / "charts/cisco-virtual-kubelet/tests/native-tas-kind-test.sh"


class NativeTASPreflightTest(unittest.TestCase):
    def run_preflight(self, context, version, expected):
        with tempfile.TemporaryDirectory() as temporary:
            kubectl = Path(temporary) / "kubectl"
            kubectl.write_text("""#!/bin/sh
case "$*" in
  'config current-context') printf '%s\\n' "$TEST_CONTEXT" ;;
  'version --client -o json') printf '{"clientVersion":{"gitVersion":"%s"}}\\n' "$TEST_VERSION" ;;
  'get namespace edge-workloads') echo 'existing test namespace' ;;
  *) echo "UNEXPECTED Kubernetes access: $*" >&2; exit 97 ;;
esac
""")
            kubectl.chmod(0o755)
            environment = dict(os.environ, PATH=temporary + os.pathsep + os.environ["PATH"],
                               TEST_CONTEXT=context, TEST_VERSION=version,
                               CVK_NATIVE_TAS_TEST_CONTEXT=expected)
            environment.pop("CVK_NATIVE_TAS_TEST_ALLOW_DISPOSABLE_CONTEXT", None)
            result = subprocess.run(["bash", str(SCRIPT)], env=environment,
                                    capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("UNEXPECTED", result.stdout + result.stderr)
            return result.stderr

    def test_refuses_non_kind_before_api_access(self):
        self.assertIn("refusing to run on non-kind", self.run_preflight("production", "v1.37.0", "kind-test"))

    def test_refuses_unselected_kind_before_api_access(self):
        self.assertIn("refusing unexpected kind", self.run_preflight("kind-other", "v1.37.0", "kind-test"))

    def test_refuses_unqualified_client_before_api_access(self):
        self.assertIn("requires pinned kubectl v1.37.0", self.run_preflight("kind-test", "v1.35.0", "kind-test"))

    def test_pinned_client_still_refuses_preexisting_fixture(self):
        self.assertIn("namespace edge-workloads already exists", self.run_preflight("kind-test", "v1.37.0", "kind-test"))


if __name__ == "__main__":
    unittest.main()
