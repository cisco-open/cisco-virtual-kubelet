#!/usr/bin/env bash
# lib/baseline.sh — shared verify-baseline helpers
#
# Sourced by every per-test verify.sh to enforce the production-
# readiness assertions named in pre-pr-test-enrichment-plan.md §5.
# Usage from a verify.sh:
#
#   . "$(dirname "$0")/../lib/baseline.sh"
#   baseline_namespace=cisco-vk-smoke
#   baseline_cr=test-04-gnmi-keyed-path
#   baseline_expected_phase=InSync
#   baseline_assert_observed_generation_synced
#   baseline_assert_phase_is "${baseline_expected_phase}"
#   baseline_assert_ready_condition_matches True Succeeded
#   baseline_assert_no_unexpected_apply_errors
#
# Each helper prints OK / FAIL on its own line and increments the
# `baseline_failures` counter on FAIL. Returns 0 always so the caller
# accumulates failures and exits at the end based on `baseline_failures`.

# shellcheck disable=SC2034  # callers may set/read these
baseline_namespace="${NAMESPACE:-cisco-vk-smoke}"
baseline_failures=0

baseline_ok()   { printf '[ OK ] %s\n' "$*"; }
baseline_fail() { printf '[FAIL] %s\n' "$*"; baseline_failures=$((baseline_failures + 1)); }

# _baseline_port_forward_scrape — kubectl port-forward + local curl.
# The cisco-vk image is gcr.io/distroless/static-debian12 (no shell,
# no curl, no wget) so the prior kubectl-exec approach to scraping
# /metrics fails on every pod. Spawn a port-forward against a random
# local port, curl /metrics from the host, then kill the forward.
# Returns the metrics body on stdout (empty on any error). All status
# noise is silenced; the caller decides what an empty result means.
_baseline_port_forward_scrape() {
  local ns="$1" pod="$2" target_port="$3"
  local local_port body fwd_pid
  # Pick a random ephemeral port in the high range to avoid clashes.
  local_port=$((RANDOM % 10000 + 40000))
  kubectl port-forward -n "${ns}" "pod/${pod}" "${local_port}:${target_port}" \
    >/dev/null 2>&1 &
  fwd_pid=$!
  # Give the forward up to 3s to bind. Poll the local socket so we
  # don't sleep blindly.
  local i
  for ((i = 0; i < 30; i++)); do
    if (echo > "/dev/tcp/127.0.0.1/${local_port}") 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  body="$(curl -s --max-time 5 "http://127.0.0.1:${local_port}/metrics" 2>/dev/null || true)"
  kill "${fwd_pid}" >/dev/null 2>&1 || true
  wait "${fwd_pid}" 2>/dev/null || true
  printf '%s' "${body}"
}

# baseline_assert_observed_generation_synced — pin §5: the CR's
# observed generation must equal the metadata generation, otherwise
# the CR's reported phase is from a stale spec and the test's
# assertions against status are meaningless.
baseline_assert_observed_generation_synced() {
  local cr="${baseline_cr:?baseline_cr must be set}"
  local meta_gen obs_gen
  meta_gen="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.metadata.generation}' 2>/dev/null)"
  obs_gen="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.status.observedGeneration}' 2>/dev/null)"
  if [[ "${meta_gen}" == "${obs_gen}" ]]; then
    baseline_ok "observedGeneration (${obs_gen}) == metadata.generation (${meta_gen})"
  else
    baseline_fail "observedGeneration=${obs_gen} != metadata.generation=${meta_gen} — status is stale"
  fi
}

# baseline_assert_phase_is — the CR's terminal phase must match.
baseline_assert_phase_is() {
  local want="${1:?usage: baseline_assert_phase_is <Phase>}"
  local cr="${baseline_cr:?baseline_cr must be set}"
  local got
  got="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.status.phase}' 2>/dev/null)"
  if [[ "${got}" == "${want}" ]]; then
    baseline_ok "phase=${want}"
  else
    baseline_fail "phase=${got}, want ${want}"
  fi
}

# baseline_assert_ready_condition_matches — Ready/<Status>/<Reason>
# must match expectation. Failure prints the message field for
# diagnosis. Reason is normalised to a stable closed set per the
# engine.
baseline_assert_ready_condition_matches() {
  local want_status="${1:?usage: baseline_assert_ready_condition_matches <Status> <Reason>}"
  local want_reason="${2:?usage: baseline_assert_ready_condition_matches <Status> <Reason>}"
  local cr="${baseline_cr:?baseline_cr must be set}"
  local got_status got_reason got_message
  got_status="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)"
  got_reason="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}' 2>/dev/null)"
  got_message="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null)"
  if [[ "${got_status}" == "${want_status}" && "${got_reason}" == "${want_reason}" ]]; then
    baseline_ok "Ready/${want_status}/${want_reason}"
  else
    baseline_fail "Ready=${got_status}/${got_reason}, want ${want_status}/${want_reason} (message: ${got_message})"
  fi
}

# baseline_assert_no_unexpected_apply_errors — any familyStatus with
# state=ApplyError fails the assertion. Tests that EXPECT ApplyError
# (none of the six release-blocker tests do today) should override
# this with a custom check.
baseline_assert_no_unexpected_apply_errors() {
  local cr="${baseline_cr:?baseline_cr must be set}"
  local errored
  errored="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" -o json 2>/dev/null \
    | jq -r '.status.familyStatus[]? | select(.state == "ApplyError") | .name' 2>/dev/null || true)"
  if [[ -z "${errored}" ]]; then
    baseline_ok "no ApplyError family statuses"
  else
    baseline_fail "ApplyError reported on families: $(echo "${errored}" | tr '\n' ' ')"
  fi
}

# baseline_assert_family_state — a specific family must be in the
# expected state with at least the expected opCount. opCount is a
# floor; the engine may emit more ops than the test expects without
# failing (e.g. a writer's idempotency check found extra leaves).
baseline_assert_family_state() {
  local family="${1:?usage: baseline_assert_family_state <family> <state> [<min-opcount>]}"
  local want_state="${2:?usage: baseline_assert_family_state <family> <state> [<min-opcount>]}"
  local min_ops="${3:-0}"
  local cr="${baseline_cr:?baseline_cr must be set}"
  local got_state got_ops
  got_state="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath="{.status.familyStatus[?(@.name=='${family}')].state}" 2>/dev/null)"
  got_ops="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath="{.status.familyStatus[?(@.name=='${family}')].opCount}" 2>/dev/null)"
  got_ops="${got_ops:-0}"
  if [[ "${got_state}" != "${want_state}" ]]; then
    baseline_fail "family ${family} state=${got_state}, want ${want_state}"
  elif (( got_ops < min_ops )); then
    baseline_fail "family ${family} opCount=${got_ops}, want >= ${min_ops}"
  else
    baseline_ok "family ${family}: state=${want_state}, opCount=${got_ops}"
  fi
}

# baseline_assert_no_stale_lease_blocked — after a successful
# recovery test (e.g. test 05 credential rotation), the CR must
# NOT be left in LeaseBlocked. Run as the last check in tests where
# LeaseBlocked is a transient observation rather than the final
# state.
baseline_assert_no_stale_lease_blocked() {
  local cr="${baseline_cr:?baseline_cr must be set}"
  local got
  got="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o jsonpath='{.status.phase}' 2>/dev/null)"
  if [[ "${got}" == "LeaseBlocked" ]]; then
    baseline_fail "CR stuck in LeaseBlocked after recovery (lease holder may be a deleted pod; check 'kubectl get lease -n cisco-vk-leases')"
  else
    baseline_ok "no stale LeaseBlocked (phase=${got})"
  fi
}

# baseline_assert_no_unexpected_drift — status.drift[] must be empty
# after a clean apply. Tests that intentionally leave drift on the CR
# (none of the six release-blocker tests today) should override.
baseline_assert_no_unexpected_drift() {
  local cr="${baseline_cr:?baseline_cr must be set}"
  local n
  n="$(kubectl get iosxeconfig "${cr}" -n "${baseline_namespace}" \
    -o json 2>/dev/null | jq '.status.drift // [] | length' 2>/dev/null || echo unknown)"
  if [[ "${n}" == "0" ]]; then
    baseline_ok "no drift entries"
  else
    baseline_fail "${n} drift entries remain after apply (run 'kubectl get iosxeconfig ${cr} -o yaml' to inspect)"
  fi
}

# baseline_assert_metric_counter — fetches a Prometheus counter from
# the cisco-vk pod's /metrics endpoint and asserts the value with
# the requested label set is at least `min`. No-op (warn-only) when
# the pod doesn't expose /metrics or curl fails.
#
# Usage:
#   baseline_assert_metric_counter \
#     cisco_vk_config_transactions_total \
#     'device="cat9k-smoke",transport="netconf",outcome="commit"' \
#     1
baseline_assert_metric_counter() {
  local metric="${1:?usage: baseline_assert_metric_counter <name> <labels> <min>}"
  local labels="${2:?usage: baseline_assert_metric_counter <name> <labels> <min>}"
  local min="${3:?usage: baseline_assert_metric_counter <name> <labels> <min>}"
  local pod
  pod="$(kubectl get pod -n "${baseline_namespace}" -l "app.kubernetes.io/instance=${DEVICE_NAME:-cat9k-smoke}" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
  if [[ -z "${pod}" ]]; then
    baseline_fail "could not locate cisco-vk pod for metric scrape"
    return
  fi
  # cisco-vk runs in a distroless static-debian12 container — no
  # shell, no wget, no curl — so kubectl-exec is unusable for the
  # scrape. Use kubectl port-forward against the pod's metrics port
  # instead. The forward terminates as soon as the local curl
  # finishes; we pick a random local port to avoid clashing with
  # parallel test runs.
  local raw
  raw="$(_baseline_port_forward_scrape "${baseline_namespace}" "${pod}" 8080)"
  if [[ -z "${raw}" ]]; then
    baseline_fail "could not scrape /metrics from pod ${pod} (does the pod expose 8080?)"
    return
  fi
  local line value
  # `|| true` keeps the helper additive when the metric label combo
  # is not present yet — `set -e -o pipefail` would otherwise abort
  # the verify script before baseline_fail logs the missing-metric.
  line="$(echo "${raw}" | grep -F "${metric}{${labels}}" | head -1 || true)"
  if [[ -z "${line}" ]]; then
    baseline_fail "metric ${metric}{${labels}} not present in /metrics scrape"
    return
  fi
  value="$(echo "${line}" | awk '{print $NF}')"
  # Simple int comparison; counter values are floats in Prom format
  # but always integer-equivalent for the counters we care about.
  value="${value%.*}"
  if (( value >= min )); then
    baseline_ok "metric ${metric}{${labels}} = ${value} (>= ${min})"
  else
    baseline_fail "metric ${metric}{${labels}} = ${value}, want >= ${min}"
  fi
}

# baseline_assert_metric_counter_zero — strict-zero variant. Used by
# the negative-control checks in test 02 (no transaction must have
# fired) and similar. Fails if the metric line exists with a non-zero
# value; passes if the line is absent (which is how Prometheus
# represents "this label combination has never been incremented").
baseline_assert_metric_counter_zero() {
  local metric="${1:?usage: baseline_assert_metric_counter_zero <name> <labels>}"
  local labels="${2:?usage: baseline_assert_metric_counter_zero <name> <labels>}"
  local pod
  pod="$(kubectl get pod -n "${baseline_namespace}" -l "app.kubernetes.io/instance=${DEVICE_NAME:-cat9k-smoke}" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
  if [[ -z "${pod}" ]]; then
    baseline_fail "could not locate cisco-vk pod for metric scrape"
    return
  fi
  local raw
  raw="$(_baseline_port_forward_scrape "${baseline_namespace}" "${pod}" 8080)"
  if [[ -z "${raw}" ]]; then
    baseline_fail "could not scrape /metrics from pod ${pod}"
    return
  fi
  local line value
  # `set -e -o pipefail` would otherwise terminate the whole verify
  # script the first time grep does not match the label combination
  # (which is exactly the negative-control case we want to assert).
  # `|| true` keeps the helper additive on grep miss.
  line="$(echo "${raw}" | grep -F "${metric}{${labels}}" | head -1 || true)"
  if [[ -z "${line}" ]]; then
    # Label combo never seen — counter has never incremented to
    # this value. That is exactly what the negative control wants.
    baseline_ok "metric ${metric}{${labels}} = 0 (label combo not present)"
    return
  fi
  value="$(echo "${line}" | awk '{print $NF}')"
  value="${value%.*}"
  if (( value == 0 )); then
    baseline_ok "metric ${metric}{${labels}} = 0 (explicit)"
  else
    baseline_fail "metric ${metric}{${labels}} = ${value}, want 0 (negative control)"
  fi
}

# baseline_summary — call at the end of verify.sh to print a
# pass/fail header and exit with the right code.
baseline_summary() {
  echo ""
  if (( baseline_failures == 0 )); then
    echo "verify: PASS"
    return 0
  else
    echo "verify: FAIL (${baseline_failures} failure$([ "${baseline_failures}" -ne 1 ] && echo s))"
    return 1
  fi
}
