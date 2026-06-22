#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "missing required environment variable: ${name}" >&2
    exit 2
  fi
}

require_env NXOS_HOST
require_env NXOS_USERNAME
require_env NXOS_PASSWORD

export RUN_LIVE_NXOS_CONFIG=1
export NXOS_LIVE_ADDRESS="${NXOS_HOST}"
export NXOS_LIVE_USERNAME="${NXOS_USERNAME}"
export NXOS_LIVE_PASSWORD="${NXOS_PASSWORD}"
export NXOS_LIVE_TRANSPORT="${NXOS_LIVE_TRANSPORT:-rest}"
export NXOS_LIVE_TLS="${NXOS_LIVE_TLS:-true}"
export NXOS_LIVE_INSECURE_SKIP_VERIFY="${NXOS_LIVE_INSECURE_SKIP_VERIFY:-true}"

echo "== NX-OS read-only DME/NX-API smoke =="
(cd "${repo_root}" && go test ./internal/drivers/nxos -run TestLiveNXOSConfigSmoke -count=1 -v)

if [[ "${RUN_LIVE_NXOS_CONFIG_WRITE:-0}" == "1" ]]; then
  echo "== NX-OS disposable VLAN DME write/verify smoke =="
  export RUN_LIVE_NXOS_CONFIG_WRITE=1
  (cd "${repo_root}" && go test ./internal/drivers/nxos -run TestLiveNXOSConfigSmoke -count=1 -v)
fi

if [[ "${RUN_NXOS_DEVICEOP_SMOKE:-0}" == "1" ]]; then
  require_env NXOS_K8S_NAMESPACE
  require_env NXOS_K8S_DEVICE
  op_name="${NXOS_DEVICEOP_NAME:-cvk-nxos-smoke-$(date +%s)}"
  command="${NXOS_DEVICEOP_COMMAND:-show version}"
  echo "== NX-OS DeviceOperation smoke (${op_name}) =="
  kubectl -n "${NXOS_K8S_NAMESPACE}" apply -f - <<EOF
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: ${op_name}
spec:
  deviceRef:
    name: ${NXOS_K8S_DEVICE}
  operation:
    kind: ShowCommand
    commands:
      - "${command}"
  ttlSecondsAfterFinished: 300
EOF
  kubectl -n "${NXOS_K8S_NAMESPACE}" wait --for=jsonpath='{.status.phase}'=Succeeded "deviceoperation/${op_name}" --timeout="${NXOS_DEVICEOP_TIMEOUT:-180s}"
  kubectl -n "${NXOS_K8S_NAMESPACE}" get "deviceoperation/${op_name}" -o yaml
fi

if [[ "${RUN_NXOS_APPHOSTING_SMOKE:-0}" == "1" ]]; then
  require_env NXOS_K8S_NAMESPACE
  require_env NXOS_K8S_DEVICE
  require_env NXOS_APP_IMAGE
  pod_name="${NXOS_APP_POD_NAME:-cvk-nxos-app-smoke-$(date +%s)}"
  echo "== NX-OS app-hosting pod smoke (${pod_name}) =="
  cleanup() {
    kubectl -n "${NXOS_K8S_NAMESPACE}" delete pod "${pod_name}" --ignore-not-found=true --wait=true
  }
  trap cleanup EXIT
  kubectl -n "${NXOS_K8S_NAMESPACE}" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${pod_name}
  labels:
    app.kubernetes.io/name: cvk-nxos-app-smoke
spec:
  nodeName: ${NXOS_K8S_DEVICE}
  restartPolicy: Never
  containers:
    - name: smoke
      image: "${NXOS_APP_IMAGE}"
EOF
  kubectl -n "${NXOS_K8S_NAMESPACE}" wait --for=condition=Ready "pod/${pod_name}" --timeout="${NXOS_APP_TIMEOUT:-600s}"
  kubectl -n "${NXOS_K8S_NAMESPACE}" get pod "${pod_name}" -o wide
fi
