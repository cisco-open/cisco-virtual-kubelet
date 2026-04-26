# Test 02 — NETCONF transactional + CLI block, engine fail-fast rejection

**§6.D.ii row:** "NETCONF transactional + CLI block rejection → `Phase=Failed`, `ErrTransactionalCLIUnsupported`, no transport-side mutation."
**Closing wave:** 7A.1 ([`../../../external-review-next-actions-response.md`](../../../external-review-next-actions-response.md) — "fix(engine): Wave 7A.1 — reject transactional+CLI before any mutation").

## What this test proves

The Wave 7A.1 fix moved the transactional-vs-CLI guard from the transport layer (where it would fire AFTER an `edit-config` had already been issued) to the engine boundary, so an `IOSXEConfig` that combines `spec.transactional: true` with a CLI-template family triggers `Phase=Failed` with `ErrTransactionalCLIUnsupported` BEFORE any RPC is sent to the device.

This is the **safest** of the six release-blocker tests by design — if the engine works correctly, the device is never contacted at all. The test exists *because* a regression here would be silent against `fake.Client` and would only surface as half-applied state on a real apiserver against a real device.

## Device surface used

**None.** The engine should reject before any RESTCONF/NETCONF/gNMI call. If the test causes any device-side change, that is itself the bug the test catches.

## Pre-state

Capture once before applying:

```sh
./pre-state.sh > pre-state.txt
```

`pre-state.sh` records the device's hostname (a noisy field that shouldn't change) and the count of any CLI-blockworthy artefacts (e.g. configured banner length). The point is: this snapshot must equal the post-test snapshot, byte-for-byte.

## Apply

```sh
kubectl apply -f 00-apply.yaml
```

This creates an `IOSXEConfig` named `test-02-cli-rejection` in `cisco-vk-smoke`, targeting the `cat9k-smoke` device, with `transactional: true` AND a CLI template that produces a CLI block. The engine should reject this combination.

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == Failed`
- `status.conditions[?(@.type=="Ready")].status == False`
- `status.conditions[?(@.type=="Ready")].reason == ErrTransactionalCLIUnsupported`
- Device-side state unchanged (hostname, banner length, etc. all match `pre-state.txt`).
- `kubectl logs` on the cisco-vk pod shows no `edit-config` or `Mutate` calls for this CR.

## Verify

```sh
./verify.sh
```

`verify.sh` checks the four assertions above. Exits 0 on success.

## Rollback

```sh
./rollback.sh
```

Just deletes the test CR. There should be nothing else to revert; if there is, the test exposed a regression — capture state and file a bug before continuing.
