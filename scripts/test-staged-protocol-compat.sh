#!/usr/bin/env bash
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Execute the actual released manager and worker gates, not reimplementations.
# No Kubernetes or device access. The checkout must contain this pinned commit
# (CI fetches it explicitly). Keep the disposable source for failure inspection.
baseline=cf33e51c8ffc6d47acb313857665366d74eefe6c
root=$(git rev-parse --show-toplevel)
git -C "$root" cat-file -e "$baseline^{commit}"
work=$(mktemp -d "${TMPDIR:-/tmp}/cvk-october-compat.XXXXXX")
git -C "$root" archive "$baseline" | tar -x -C "$work"
cp "$root/scripts/testdata/october_staged_protocol_test.go" \
  "$work/internal/provider/softwareupgrade/"
cp "$root/scripts/testdata/october_manager_protocol_test.go" \
  "$work/internal/controller/"
cp "$root/scripts/testdata/october_device_ca_contract_test.go" \
  "$work/cmd/cisco-vk/"
printf 'Released manager/worker commit: %s\nRetained test source: %s\n' "$baseline" "$work"
cd "$work"
go test -count=1 ./internal/provider/softwareupgrade ./internal/controller ./cmd/cisco-vk \
  -run '^TestOctober(WorkerStagedProtocolFence|ManagerRejectsNewWorkerProtocols|WorkerRejectsDeviceCAContract)$' -v
