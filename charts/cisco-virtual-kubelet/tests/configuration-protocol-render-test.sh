#!/usr/bin/env bash
set -euo pipefail
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch_dir="$(mktemp -d)"
trap 'rm -rf -- "$scratch_dir"' EXIT
helm template cvk "$chart_dir" --kube-version 1.28.0 >"$scratch_dir/default.yaml"
if grep -Fq -- '--configuration-reservation-protocol=' "$scratch_dir/default.yaml"; then
  echo "default deployment changed protocol selection" >&2
  exit 1
fi
managed=(--kube-version 1.35.0 --set topology.enabled=true --set controller.leaderElect=true --set rbac.profile=strict)
group=(--set topology.configurationReservationProtocol=controller-configuration-group-v1)
helm template cvk "$chart_dir" "${managed[@]}" "${group[@]}" --set topology.workerAccounts.networkManagement.accessMode=readWrite >"$scratch_dir/group.yaml"
grep -Fq -- '--configuration-reservation-protocol=controller-configuration-group-v1' "$scratch_dir/group.yaml"
for validation in schema template; do
  for scenario in disabled readonly unknown; do
    args=("${managed[@]}" "${group[@]}")
    if [[ "$validation" == template ]]; then args+=(--skip-schema-validation); fi
    case "$scenario" in
      disabled) args+=(--set topology.enabled=false --set topology.workerAccounts.networkManagement.accessMode=readWrite);;
      readonly) args+=(--set topology.workerAccounts.networkManagement.accessMode=readOnly);;
      unknown) args+=(--set topology.configurationReservationProtocol=unknown);;
    esac
    if helm template cvk "$chart_dir" "${args[@]}" >"$scratch_dir/rejected.yaml" 2>"$scratch_dir/error.txt"; then
      echo "invalid $validation/$scenario protocol configuration accepted" >&2
      exit 1
    fi
    grep -Eq 'configurationReservationProtocol|group configuration protocol|readWrite|topology.enabled' "$scratch_dir/error.txt"
  done
done
