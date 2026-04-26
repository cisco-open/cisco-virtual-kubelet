#!/usr/bin/env bash
# Test 07 verify. Asserts the §5 baseline plus the writeStartup-
# specific assertions: SaveStartupOK event AND the
# save_startup_total{ok} counter incremented.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-07-write-startup"
EXPECTED_DESC="cisco-vk release-blocker test 07 — writeStartup"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_family_state interface_loopback InSync 1
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift

# SaveStartup metric — the test's headline assertion. Pre-fix
# (no metric) the only signal that save-config ran was a
# Kubernetes event, which is best-effort and could be lost on
# rapid reconciles.
baseline_assert_metric_counter \
  cisco_vk_config_save_startup_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="ok"' \
  1 || \
  baseline_assert_metric_counter \
    cisco_vk_config_save_startup_total \
    'device="'"${DEVICE_NAME}"'",transport="restconf",outcome="ok"' \
    1

# No save-startup failures should be visible in the metric scrape
# for this device.
baseline_assert_metric_counter_zero \
  cisco_vk_config_save_startup_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="failed"'
baseline_assert_metric_counter_zero \
  cisco_vk_config_save_startup_total \
  'device="'"${DEVICE_NAME}"'",transport="restconf",outcome="failed"'

# Kubernetes-event check: a SaveStartupOK event must exist for this
# CR. Events are best-effort but the metric above is the durable
# proof; this assertion catches a regression where the event
# emission is dropped from the recorder while the metric path
# stays wired.
events="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=SaveStartupOK \
  -o jsonpath='{.items[*].message}' 2>/dev/null || true)"
if [[ -n "${events}" ]]; then
  baseline_ok "SaveStartupOK event present: ${events}"
else
  baseline_fail "no SaveStartupOK event on IOSXEConfig/${TEST_CR}"
fi

# Device-side running-config check.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

actual_desc="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9997" \
  | jq -r '.["Cisco-IOS-XE-native:Loopback"].description // ""')"
if [[ "${actual_desc}" != "${EXPECTED_DESC}" ]]; then
  baseline_fail "running-config Loopback9997 description=${actual_desc}, want ${EXPECTED_DESC}"
else
  baseline_ok "running-config Loopback9997 description matches"
fi

# Startup-config check. RESTCONF doesn't expose startup-config
# directly via the native model; operator should attach a
# `show startup-config | include Loopback9997` excerpt to the
# evidence bundle. The script accepts a STARTUP_CONFIG_INCLUDES
# env override for an automated attestation.
if [[ -n "${STARTUP_CONFIG_INCLUDES:-}" ]]; then
  if echo "${STARTUP_CONFIG_INCLUDES}" | grep -q "9997"; then
    baseline_ok "startup-config attestation includes Loopback9997"
  else
    baseline_fail "STARTUP_CONFIG_INCLUDES set but does not contain 'Loopback9997' / '9997'"
  fi
else
  echo "INFO: startup-config check skipped — set STARTUP_CONFIG_INCLUDES to your 'show startup-config | include 9997' output for an explicit assertion"
fi

baseline_summary
exit "${baseline_failures}"
