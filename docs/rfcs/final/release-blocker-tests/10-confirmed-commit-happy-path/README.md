# Test 10 — confirmed-commit happy path (Wave 10 variation)

**§6.D.ii row added per Wave-10 variation matrix (option C, post-Wave-10.4):**
"Confirmed-commit auto-revert path engages AND succeeds end-to-end. Complements test 08 by proving the safety net is *enabled*, not just that it fires on failure."
**Closing waves:** 10.1 (transport `ConfirmedCommitter`) + 10.2 (engine state machine).

## What this test proves

Test 08 proves the safety net **fires** when running-verify fails. This test proves the safety net **engages cleanly** when running-verify passes — i.e. the auto-revert path is the default for `confirmTimeoutSeconds > 0` against a NETCONF transport with `:confirmed-commit:1.0` advertised, and a clean change rolls all the way through to confirmed.

Specifically:

1. `Result.ConfirmedCommitUsed == true` — engine took the auto-revert path, not the plain-Commit fallback.
2. `cisco_vk_config_transactions_total{outcome="confirmed"}` increments by 1.
3. A Kubernetes Normal event with reason `ConfirmedCommitUsed` appears on the CR (Wave 10.4 recorder integration).
4. The change actually applies — no auto-revert happens — because running-verify reads clean from running.

If this test fails, the most likely cause is that operators who *intend* to use confirmed-commit are silently falling back to plain Commit (tests 08 and 11 cover the *expected* fallback paths; this test catches the case where fallback happens *unexpectedly*).

## Device surface used

**Loopback 9995** — distinct from test 01's 9999, test 03's 9998, test 07's 9997, test 09's 9996. The test adds a description and an IPv4 address. Removable in one RESTCONF DELETE.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Asserts Loopback 9995 is absent. Records the pre-test value of `cisco_vk_config_transactions_total{outcome="confirmed"}` so verify can assert a +1 delta.

## Apply

```sh
kubectl apply -f 00-apply.yaml
# Wait for status.phase=InSync.
kubectl get iosxeconfig test-10-confirmed-commit-happy -n cisco-vk-smoke -w
```

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == InSync`.
- A `ConfirmedCommitUsed` Normal event on the CR.
- `cisco_vk_config_transactions_total{outcome="confirmed"}` incremented by 1.
- `cisco_vk_config_transactions_total{outcome="commit"}` did NOT increment (plain Commit was bypassed).
- Loopback 9995 is on the device with the test description and IP.

## Verify

```sh
bash ./verify.sh
```

## Rollback

```sh
bash ./rollback.sh
```
