# Test 11 — confirmed-commit fallback over RESTCONF (Wave 10 variation)

**§6.D.ii row added per Wave-10 variation matrix:**
"CR opts in to confirmed-commit but the transport is RESTCONF. Engine falls back to plain Commit and surfaces a Warning event with the reason `transport does not implement ConfirmedCommitter`."
**Closing wave:** 10.2 (engine fallback path) + 10.4 (recorder integration).

## What this test proves

The most important *backward-compat* assertion in the Wave 10 design: a CR that opts in to confirmed-commit against a transport that cannot deliver it does NOT silently lose the auto-revert protection — instead the engine emits a Kubernetes Warning event explicitly naming the reason. Operators can `kubectl events` and see why their safety net didn't engage.

This test is the only Wave-10 variation that **exercises a different transport** (RESTCONF rather than NETCONF). It needs a CiscoDevice configured with `spec.xe.transport: restconf` (the default for new CRs) — most lab setups already have one of these alongside the NETCONF-enabled `cat9k-smoke`. If your lab only has NETCONF-configured devices, the operator can flip the test device's transport temporarily for the test (see "Pre-state" below).

This test does NOT require breaking the management session or any device-side disruption. It's the safest of the new Wave-10 tests.

## Device surface used

**Loopback 9994** — distinct from tests 01/03/07/09/10. Description + IP. Fully reverted by the rollback step.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Asserts:
- Loopback 9994 is absent on the device.
- The CiscoDevice's resolved transport is RESTCONF (not NETCONF). If the existing `cat9k-smoke` is NETCONF, the runbook directs the operator to either (a) create a sibling `cat9k-smoke-restconf` CiscoDevice pointing at the same physical device with RESTCONF, or (b) skip this test and rely on envtest coverage (the engine's fallback decision is unit-tested under `TestConfirmedCommitFallbackWhenTransportLacksInterface`).

## Apply

```sh
kubectl apply -f 00-apply.yaml
# Wait for status.phase=InSync.
kubectl get iosxeconfig test-11-restconf-fallback -n cisco-vk-smoke -w
```

The CR has `confirmTimeoutSeconds=30` and `transactional=false` (RESTCONF has no candidate datastore so transactional=true would itself error). The engine should:

1. Detect that the transport is not transactional (`transactional=false` on the CR). For confirmed-commit, this is also a fallback condition.
2. Surface `Result.ConfirmedCommitFallback="non-transactional reconcile"`.
3. Take the standard non-transactional Mutate path.
4. Reconcile reaches `Phase=InSync`; CR has a Warning event with reason `ConfirmedCommitFallback`.

A second flavour of this test would set `transactional=true` against a RESTCONF transport, which would error at the engine level entirely. Today the engine returns `Phase=Failed, Err=StartTransaction: ErrUnsupported` in that case — covered by the existing `TestNonTransactionalSkipsTransactionLifecycle` unit test rather than a live retest.

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == InSync` (the device-side reconcile succeeds; the *fallback* doesn't fail the apply).
- A Warning event with reason `ConfirmedCommitFallback` and message containing `non-transactional reconcile`.
- NO `ConfirmedCommitUsed` event.
- `cisco_vk_config_transactions_total{...,outcome="confirmed"}` did NOT increment.

## Verify

```sh
bash ./verify.sh
```

## Rollback

```sh
bash ./rollback.sh
```
