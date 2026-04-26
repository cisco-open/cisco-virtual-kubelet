# Test 01 — NETCONF transactional commit, structured-only intent

**§6.D.ii row:** "NETCONF transactional commit, structured-only intent → `Phase=InSync` end-to-end."
**Closing wave:** 1A-fu ([`../../../external-review-followup-response.md`](../../../external-review-followup-response.md) — "fix(transport, engine): Wave 1A-fu — NETCONF candidate Fetch + CLI in tx scope").

## What this test proves

When `spec.transactional: true` AND every family is structured (no CLI templates), the engine must drive the candidate-datastore + commit lifecycle on a NETCONF-capable transport:

1. `transport.StartTransaction()` opens NETCONF candidate datastore.
2. Per-family Fetch reads from candidate (Wave 1A-fu's `TxFetcher` path) so the verify phase sees in-flight writes.
3. Mutate edits emit to candidate.
4. `Commit()` issues `<commit/>` RPC.
5. On any failure, `Discard()` is invoked.

Pre-Wave-1A-fu, the verify phase Fetch went to the running datastore mid-transaction, so ops just queued in candidate didn't roundtrip — the verify either saw stale state or required a re-Fetch after commit. The fix routes Fetch through a candidate-aware `TxFetcher` when the transport implements it.

## Device surface used

**One Loopback interface.** The test creates `Loopback9999` with a description string. Loopback 9999 is chosen because it is far outside the typical lab range (0–100) — confirm it is not in use before applying. Rollback deletes it.

The test also sets `spec.transactional: true` and `spec.writeStartup: false` so the post-commit running-config diff is the only thing exercised; the startup-config copy is a separate path (covered by other unit tests).

## Pre-test prerequisites

**The device must have NETCONF/830 enabled** for this test to do anything meaningful. Confirm with:

```sh
nc -zv 10.1.1.1 830
```

If 830 is closed/filtered, the test will fall back to the RESTCONF transport (which has no candidate datastore), and the closing-wave assertion (`StartTransaction` + `Commit` actually run) cannot be exercised. Either enable NETCONF on the device or skip this test and document.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Captures the device's current Loopback list. Verify there is no `Loopback9999` in pre-state before proceeding.

## Apply

```sh
kubectl apply -f 00-apply.yaml
```

Creates `IOSXEConfig test-01-netconf-transactional` with `spec.transactional: true`, `managedFamilies: [interface_loopback]`, and an inline source declaring `Loopback9999` with a test description.

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == InSync`.
- Device-side: `Loopback9999` exists with the test description, IP `10.255.255.99/32`.
- cisco-vk pod logs show, in order: `StartTransaction`, candidate Fetch (read-back of the Loopback list including `Loopback9999`), Mutate, `Commit`. No `Discard`.
- The `cisco_vk_engine_transactions_total{outcome="commit"}` counter increments by 1.

## Verify

```sh
bash ./verify.sh
```

## Rollback

```sh
bash ./rollback.sh
```

Deletes the test CR. The deletion path drives the engine's empty-intent reconcile for `interface_loopback` with `pruneOnRelinquish=true` *for this CR's family list* — which removes `Loopback9999` cleanly. The script then verifies `Loopback9999` is no longer present on the device.
