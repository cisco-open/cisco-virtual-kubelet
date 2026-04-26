# Test 13 — Expected outcome

## Phase 1 (establish state, both safety nets on)

After `kubectl apply -f 00-apply-establish.yaml`:

```yaml
status:
  phase: InSync
  observedGeneration: 1
  familyStatus:
    - name: vlan
      state: InSync
    - name: vrf
      state: InSync
    - name: interface_loopback
      state: InSync
```

Events:
- `ConfirmedCommitUsed` (Normal) — auto-revert path engaged and confirmed.
- `AppliedSuccess` for the family-level applies.
- NO `ConfirmedCommitFallback`.

Metrics delta:
- `cisco_vk_config_transactions_total{...,outcome="confirmed"}` += 1
- `cisco_vk_config_transactions_total{...,outcome="commit"}` unchanged
- `cisco_vk_config_mutate_ops_total{transport="netconf",verb="REPLACE"}` += several (one per family + per-key)

Device-side: VLAN 997, VRF TEST-13-VRF, Loopback 9993 all present.

## Phase 2 (atomic remove, both safety nets on)

After `kubectl apply -f 01-apply-empty.yaml`:

```yaml
status:
  phase: InSync
  observedGeneration: 2
  familyStatus:
    - name: vlan
      state: InSync
      opCount: 1     # delete op for VLAN 997
    - name: vrf
      state: InSync
      opCount: 1     # delete op for TEST-13-VRF
    - name: interface_loopback
      state: InSync
      opCount: 1     # delete op for Loopback 9993
```

Events:
- A second `ConfirmedCommitUsed` (Normal) — atomic removal also went through the auto-revert path successfully.

Metrics delta from phase 1 → phase 2:
- `outcome="confirmed"` += 1 (now +2 total since pre-test)
- `outcome="auto_reverted"` unchanged at 0

Device-side: all three test entities absent. Cross-family ordering correct — at no intermediate point should the device have a Loopback 9993 referencing a deleted VRF.

## Combined-mode invariants

This test's headline assertions, beyond the per-phase ones:

1. **Two `ConfirmedCommitUsed` events on the same CR.** Each phase took the auto-revert path. Pre-Wave-10 there was no event; one event = one phase fell back; two events = both safety nets engaged on both phases.
2. **Zero `ConfirmedCommitFallback` events.** If either phase fell back (RESTCONF transport regression, capability not advertised, anything else), the safety net wasn't fully engaged.
3. **Zero `outcome="auto_reverted"` increments.** Both phases should reach ConfirmCommit cleanly. A non-zero auto-revert count means running-verify failed at some point — the test should fail and operator should investigate.

## What this proves

- Both Wave 10 features compose correctly. The engine's `confirmedCommitDecision` and `runningVerify` work alongside `AtomicReplace=true` and the cross-family `FamilyOrder` topo-sort.
- The recommended operator default for risk-sensitive CRs (`atomicReplace=true, confirmTimeoutSeconds=30, transactional=true`) actually delivers the protection the RFC promises end-to-end.
- An atomic-removal transaction that broke the management session would also auto-revert — combining the two features doesn't compromise either's safety net.
