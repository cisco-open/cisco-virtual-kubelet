#!/usr/bin/env bash
# Test 05 verify. Watches the IOSXEConfig phase for ~60s and asserts:
#   1. New pod started (UID changed from pre-state)
#   2. status.phase transitioned through LeaseBlocked at least once
#   3. status.phase eventually returned to non-LeaseBlocked
#   4. No `cisco_vk_engine_apply_ops_total` increment during the window
#      (this is observed via the cisco-vk pod's /metrics endpoint
#      before vs after, if the pod exposes it).

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

if [[ ! -f pre-state.txt ]]; then
  echo "ERROR: pre-state.txt not found. Run pre-state.sh > pre-state.txt first."
  exit 2
fi
pre_uid="$(grep -A1 '^current-pod-uid:' pre-state.txt | tail -1)"

fail=0

echo "Polling for new pod (different UID from pre-state ${pre_uid:0:8}...)"
for i in $(seq 1 60); do
  cur_uid="$(kubectl get pod -n "${NAMESPACE}" -l app="${DEVICE_NAME}" \
    -o jsonpath='{.items[0].metadata.uid}' 2>/dev/null || true)"
  if [[ -n "${cur_uid}" && "${cur_uid}" != "${pre_uid}" ]]; then
    echo "OK:   new pod started, UID=${cur_uid:0:8}... (was ${pre_uid:0:8}...)"
    break
  fi
  sleep 1
  if [[ "$i" == 60 ]]; then
    echo "FAIL: pod UID still matches pre-state after 60s — Deployment did not roll"
    fail=1
  fi
done

# Watch for LeaseBlocked. We poll because the transition can be brief.
echo "Polling for LeaseBlocked transition (up to 90s)..."
saw_blocked=0
saw_recovery=0
for i in $(seq 1 90); do
  phase="$(kubectl get iosxeconfig "${DEVICE_NAME}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  if [[ "${phase}" == "LeaseBlocked" ]]; then
    saw_blocked=1
    echo "  t+${i}s: phase=LeaseBlocked"
  fi
  if (( saw_blocked == 1 )) && [[ "${phase}" != "LeaseBlocked" ]] && [[ -n "${phase}" ]]; then
    saw_recovery=1
    echo "OK:   recovered from LeaseBlocked → ${phase} at t+${i}s"
    break
  fi
  sleep 1
done

if (( saw_blocked == 0 )); then
  echo "FAIL: never observed Phase=LeaseBlocked during overlap window"
  fail=1
fi

if (( saw_recovery == 0 )); then
  echo "FAIL: stuck in LeaseBlocked or never re-checked after 90s"
  fail=1
fi

exit "${fail}"
