# Test 11 — RESTCONF fallback Warning event

The Wave 10 backward-compatibility safety net: when a CR has
`spec.confirmTimeoutSeconds` set but the transport doesn't have a
candidate datastore (RESTCONF), the engine takes the non-transactional
path and emits a clear Warning event so the operator knows the
auto-revert safety net isn't engaged.

## Reproducer

```
kubectl apply -f docs/rfcs/final/release-blocker-tests/11-confirmed-commit-restconf-fallback/00-apply.yaml
```

with `cat9k-smoke` CiscoDevice using `spec.transport: restconf` and
the engine running on the `:v4` image.

## Observed (kubectl describe iosxeconfig test-11-restconf-fallback)

```
Events:
  Type     Reason                    Message
  ----     ------                    -------
  Normal   FamilySkipped             family "interface_loopback": family leased by …
  Warning  ApplyFailed               family "interface_loopback": Apply: op[0] MERGE … 400 Bad Request: unknown-element: ipv4_address …
  Warning  ReconcileFailed           one or more families failed to reconcile
  Warning  ConfirmedCommitFallback   spec.confirmTimeoutSeconds set but auto-revert path unavailable: non-transactional reconcile — fell back to plain Commit
```

## Verdict

**PASS** for the Wave 10 safety net (ConfirmedCommitFallback event present
with the expected reason). The ApplyFailed event is a separate finding —
the writer's `ipv4_address` leaf name doesn't match the device's
Cisco-IOS-XE-native YANG schema (see SUMMARY.md bug #3). The
fallback Warning event behavior is independent of whether the apply
itself succeeds.

The `5x over 30s` aggregation in the Events table indicates the engine
re-fired the Warning on each of the five reconcile attempts — the
event isn't suppressed once-per-CR-lifetime; the operator continues to
get the signal as long as the CR is mis-configured.
