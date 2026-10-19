#!/usr/bin/env bash
set -euo pipefail
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch_dir="$(mktemp -d)"
trap 'rm -rf -- "$scratch_dir"' EXIT
helm template cvk "$chart_dir" --kube-version 1.28.0 >"$scratch_dir/default.yaml"
if grep -Fq -- '--enable-controller-metadata-handoff' "$scratch_dir/default.yaml"; then
  echo 'default deployment enabled metadata execution' >&2
  exit 1
fi
managed=(--kube-version 1.35.0 --set topology.enabled=true --set controller.leaderElect=true --set rbac.profile=strict --set config.leaseNamespace=leases --set topology.controllerMetadataHandoff=true)
# Metadata does not require a read-write network profile or a device reservation.
helm template cvk "$chart_dir" "${managed[@]}" --set topology.workerAccounts.networkManagement.accessMode=readOnly >"$scratch_dir/enabled.yaml"
grep -Fq -- '--enable-controller-metadata-handoff' "$scratch_dir/enabled.yaml"
for validation in schema template; do
  for scenario in disabled leader namespace aggregated; do
    args=("${managed[@]}")
    if [[ "$validation" == template ]]; then args+=(--skip-schema-validation); fi
    case "$scenario" in
      disabled) args+=(--set topology.enabled=false);;
      leader) args+=(--set controller.leaderElect=false);;
      namespace) args+=(--set config.leaseNamespace=);;
      aggregated) args+=(--set aggregator.enabled=true);;
    esac
    if helm template cvk "$chart_dir" "${args[@]}" >"$scratch_dir/rejected.yaml" 2>"$scratch_dir/error.txt"; then
      echo "invalid $validation/$scenario metadata installation accepted" >&2
      exit 1
    fi
  done
done
