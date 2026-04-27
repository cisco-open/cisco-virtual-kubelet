#!/usr/bin/env bash
# preflight.sh — release-blocker-tests preflight gate
#
# Runs every check the pre-PR test enrichment plan §2.2 names. Fails
# the maintenance window before any device mutation if any precondition
# is missing. The output should be saved as the first artefact in the
# evidence bundle; see RUNBOOK §2.
#
# Usage (must be the first script run in any maintenance window):
#
#   ./preflight.sh                                # all checks (default)
#   ./preflight.sh --skip-netconf                 # skip NETCONF/830 reachability
#   ./preflight.sh --skip-gnmi                    # skip gNMI/57400 reachability
#   ./preflight.sh --intf-approved=Gi1/0/24       # explicit confirmation that the
#                                                 # test 04 target interface is unused
#
# Exits 0 only if every applicable check passes. Any failure prints a
# clear remediation hint and exits non-zero.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
EXPECTED_KUBE_CONTEXT="${EXPECTED_KUBE_CONTEXT:-kind-kind}"

SKIP_NETCONF=0
SKIP_GNMI=0
INTF_APPROVED=""

for arg in "$@"; do
  case "${arg}" in
    --skip-netconf) SKIP_NETCONF=1 ;;
    --skip-gnmi)    SKIP_GNMI=1 ;;
    --intf-approved=*) INTF_APPROVED="${arg#*=}" ;;
    --help|-h)
      grep '^#' "$0" | sed 's/^# \?//' | head -25
      exit 0
      ;;
    *) echo "unknown arg: ${arg}" >&2; exit 2 ;;
  esac
done

fail=0
note() { printf '[ OK ] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*"; }
err()  { printf '[FAIL] %s\n' "$*"; fail=1; }

echo "preflight.sh — $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
echo "namespace=${NAMESPACE} device=${DEVICE_NAME} expected-context=${EXPECTED_KUBE_CONTEXT}"
echo ""

# ── 1. Required tools on PATH ────────────────────────────────────────
for tool in kubectl jq curl nc go helm; do
  if command -v "${tool}" >/dev/null 2>&1; then
    note "tool present: ${tool}"
  else
    err "tool missing: ${tool} (install before running)"
  fi
done

# ── 2. kubectl context matches expectation ──────────────────────────
if cur_ctx="$(kubectl config current-context 2>/dev/null)"; then
  if [[ "${cur_ctx}" == "${EXPECTED_KUBE_CONTEXT}" ]]; then
    note "kubectl context: ${cur_ctx}"
  else
    err "kubectl context is '${cur_ctx}', expected '${EXPECTED_KUBE_CONTEXT}'. Set EXPECTED_KUBE_CONTEXT or 'kubectl config use-context ${EXPECTED_KUBE_CONTEXT}'."
  fi
else
  err "kubectl current-context unavailable; check ~/.kube/config"
fi

# ── 3. CiscoDevice exists and is Ready ──────────────────────────────
if cd_phase="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.phase}' 2>/dev/null)"; then
  if [[ "${cd_phase}" == "Ready" ]]; then
    note "CiscoDevice ${DEVICE_NAME} phase: Ready"
  else
    err "CiscoDevice ${DEVICE_NAME} phase: ${cd_phase} (must be Ready before testing)"
  fi
else
  err "CiscoDevice ${DEVICE_NAME} not found in namespace ${NAMESPACE}"
fi

# ── 4. cisco-vk pod is Running ──────────────────────────────────────
pod_ready="$(kubectl get pod -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${DEVICE_NAME}" \
  -o jsonpath='{.items[0].status.containerStatuses[0].ready}' 2>/dev/null || echo false)"
if [[ "${pod_ready}" == "true" ]]; then
  note "cisco-vk pod is Ready"
else
  err "cisco-vk pod not Ready in namespace ${NAMESPACE} (label app.kubernetes.io/instance=${DEVICE_NAME})"
fi

# ── 5. Required env: device password ────────────────────────────────
if [[ -n "${CVK_CONFIG_LINT_PASSWORD:-}" ]]; then
  note "CVK_CONFIG_LINT_PASSWORD is set (length=${#CVK_CONFIG_LINT_PASSWORD})"
else
  err "CVK_CONFIG_LINT_PASSWORD is unset; export it before running fetch/verify"
fi

# ── 6. Device address resolvable ────────────────────────────────────
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}' 2>/dev/null || true)"
if [[ -n "${ADDR}" ]]; then
  note "device address: ${ADDR}"
else
  err "could not resolve spec.address for ${NAMESPACE}/${DEVICE_NAME}"
fi

# ── 7. Transport reachability (per test relevance) ──────────────────
# RESTCONF/443 must always be reachable.
if [[ -n "${ADDR}" ]] && timeout 5 bash -c "echo > /dev/tcp/${ADDR}/443" 2>/dev/null; then
  note "TCP/443 (RESTCONF) reachable on ${ADDR}"
else
  err "TCP/443 (RESTCONF) NOT reachable on ${ADDR:-<unknown>}; tests 04, 06 cannot run"
fi

if [[ "${SKIP_NETCONF}" -eq 0 ]]; then
  if [[ -n "${ADDR}" ]] && timeout 5 bash -c "echo > /dev/tcp/${ADDR}/830" 2>/dev/null; then
    note "TCP/830 (NETCONF) reachable on ${ADDR}"
  else
    err "TCP/830 (NETCONF) NOT reachable on ${ADDR:-<unknown>}; test 01 (and 07 if NETCONF) cannot run. Pass --skip-netconf if NETCONF is intentionally disabled."
  fi
fi

if [[ "${SKIP_GNMI}" -eq 0 ]]; then
  if [[ -n "${ADDR}" ]] && timeout 5 bash -c "echo > /dev/tcp/${ADDR}/57400" 2>/dev/null; then
    note "TCP/57400 (gNMI) reachable on ${ADDR}"
  else
    err "TCP/57400 (gNMI) NOT reachable on ${ADDR:-<unknown>}; test 04 cannot run with gNMI proof. Pass --skip-gnmi if gNMI is intentionally disabled."
  fi
fi

# ── 8. Test 04 interface explicit approval ──────────────────────────
if [[ -z "${INTF_APPROVED}" ]]; then
  err "test 04 target interface not approved. Pass --intf-approved=<Gi*/*/*> to confirm the operator has verified the chosen interface is unused. Default in 00-apply.yaml is GigabitEthernet1/0/24 (Cat9300 line-card numbering); substitute via TEST_INTF_NAME or sed the manifest."
else
  note "test 04 interface approved by operator: ${INTF_APPROVED}"
fi

# ── 9. No other IOSXEConfig owns test families on the device ────────
# Pull every IOSXEConfig targeting this device and union their
# managedFamilies. If any of the families this maintenance window
# will touch (banner, vlan, interface_loopback, interface_ethernet)
# are already owned by a non-test CR, the lease layer will surface
# the conflict but the operator should know up front.
TEST_FAMILIES="banner vlan interface_loopback interface_ethernet"
existing_owners="$(kubectl get iosxeconfig -n "${NAMESPACE}" -o json 2>/dev/null \
  | jq -r --arg dev "${DEVICE_NAME}" '
      .items[]
      | select(.spec.deviceRef.name == $dev)
      | select(.metadata.labels["cisco.vk/release-blocker-test"] == null)
      | "\(.metadata.name) -> \(.spec.managedFamilies | join(","))"
    ' 2>/dev/null || true)"
if [[ -z "${existing_owners}" ]]; then
  note "no existing non-test IOSXEConfig CRs target ${DEVICE_NAME}"
else
  for fam in ${TEST_FAMILIES}; do
    if echo "${existing_owners}" | grep -q ",\?${fam}\(,\|\$\)"; then
      warn "family ${fam} is already owned: ${existing_owners}"
    fi
  done
fi

# ── 10. Static gates passable ───────────────────────────────────────
if (cd "$(git -C "$(pwd)" rev-parse --show-toplevel 2>/dev/null || echo .)" \
    && GOCACHE=/tmp/cvk-gocache go vet ./... >/dev/null 2>&1); then
  note "go vet ./... clean"
else
  warn "go vet ./... reported issues; address before promotion (run manually for detail)"
fi

# ── Summary ─────────────────────────────────────────────────────────
echo ""
if [[ "${fail}" -eq 0 ]]; then
  echo "preflight: PASS — proceed with the runbook"
  exit 0
else
  echo "preflight: FAIL — fix the [FAIL] items above before running any test"
  exit 1
fi
