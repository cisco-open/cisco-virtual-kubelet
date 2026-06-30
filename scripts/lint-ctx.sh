#!/usr/bin/env bash
# Copyright 2026 Cisco Systems Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

# Lightweight guard for the OTel context-propagation audit. New reconciler,
# provider, and driver code should derive child contexts from the caller's ctx
# rather than using context.Background(), otherwise spans are orphaned.
#
# Existing deliberate shutdown/test boundaries are allowlisted here; keep this
# script small and explicit so reviewers can reason about every exception.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

rg -n 'context\.Background\(\)' \
  cmd internal api tools \
  --glob '*.go' \
  --glob '!**/*_test.go' >"$tmp" || true

allowed='(shutdownCtx|context\.WithTimeout\(context\.Background\(\)|context\.WithCancel\(context\.Background\(\)|signal\.NotifyContext\(context\.Background\(\)|runVirtualKubelet|NewOTELTopologyExporter|SetupSignalHandler|manager\.go|providers\.go|providerserver\.Serve|crd_drift_check\.go|config_reconciler_controller\.go|iosxetelemetry_reconciler\.go|subscriber\.go|correlation\.go)'

if grep -Ev "$allowed" "$tmp"; then
  echo "context propagation lint failed: derive from the caller ctx or add a narrow allowlist reason" >&2
  exit 1
fi
