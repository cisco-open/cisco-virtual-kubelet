# Test 11 — Expected outcome

## Phase + family status

```yaml
status:
  phase: InSync
  observedGeneration: 1
  familyStatus:
    - name: interface_loopback
      state: InSync
      opCount: 1
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
```

The reconcile reaches InSync — the fallback to plain RESTCONF behaviour does NOT fail the apply.

## Kubernetes events on the CR

```sh
kubectl events -n cisco-vk-smoke --for IOSXEConfig/test-11-restconf-fallback --sort-by=.lastTimestamp
```

A Warning event with reason `ConfirmedCommitFallback` and message:

```
spec.confirmTimeoutSeconds set but auto-revert path unavailable: non-transactional reconcile — fell back to plain Commit
```

Plus a Normal event with reason `AppliedSuccess` for the family-level apply (existing behaviour).

There must NOT be a Normal `ConfirmedCommitUsed` event — that would mean the auto-revert path engaged, which is impossible here.

## Metrics

```
cisco_vk_config_transactions_total{...,outcome="confirmed"}     unchanged (no increment)
cisco_vk_config_transactions_total{...,outcome="commit"}         unchanged (RESTCONF doesn't go through the transactional commit path; non-transactional reconcile uses per-Op Mutate only)
cisco_vk_config_mutate_ops_total{...,transport="restconf",verb="REPLACE"}  +1 (or MERGE)
```

## Device-side state

Loopback 9994 exists with the test description and IP. Same shape as test 01's Loopback 9999 but on the non-transactional path.

## What this proves

- **Operators see the fallback** via a standard Kubernetes Warning event. They can `kubectl events --field-selector reason=ConfirmedCommitFallback -A` cluster-wide to find every CR that opted in but didn't get the safety net.
- **The reconcile still succeeds.** Wave 10 fallback is graceful — it does NOT block the apply. This is the backward-compat invariant.
- **No silent loss of intent.** Pre-Wave-10.4, an operator setting `confirmTimeoutSeconds` against a RESTCONF transport would have seen no signal at all that their auto-revert request was ignored. The Warning event is the post-Wave-10.4 contract.

## What would fail

| Failure | Likely cause |
|---|---|
| `Phase=Failed` | Engine errored on the non-transactional + confirmTimeoutSeconds combination. Check the engine's `else if !transactional && res.ConfirmTimeoutSeconds > 0` branch — it should set the fallback reason and proceed, not fail. |
| No `ConfirmedCommitFallback` event | Recorder integration regressed (Wave 10.4). Check `internal/provider/config_reconciler.go`'s `emitEvents` switch on `result.ConfirmedCommitFallback`. |
| `ConfirmedCommitUsed` event present | Engine somehow took the auto-revert path despite RESTCONF + non-transactional. Critical regression — investigate `confirmedCommitDecision` ordering. |
