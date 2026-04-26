# Test 05 — Expected outcome

## Sequence (with rough timings)

- **t=0**: operator runs `kubectl annotate secret cat9k-creds ...`
- **t≈0–2s**: controller observes the Secret event, fetches the current `metadata.resourceVersion`, stamps it onto the per-device Deployment's pod-template annotation `cisco.vk/credential-resource-version`. Deployment rolls.
- **t≈2–10s**: new pod (`cat9k-smoke-vk-<new-replicaset-hash>-<new-pod-suffix>`) starts. Old pod begins terminating.
- **t≈5–15s** (overlap): both pods exist briefly. The old pod's lease (holder `cisco-vk-smoke/cat9k-smoke#<old-pod-uid>`) is still valid until TTL expiry. The new pod attempts to acquire family leases, finds them held, reports `Phase=LeaseBlocked` for the affected `IOSXEConfig` CR.
- **t≈15–45s**: old pod terminates; its lease renewal stops; lease eventually expires. New pod's next reconcile (sub-TTL requeue at 15s) succeeds in acquiring the lease, returns to whatever the steady-state phase is (likely `InSync` or `Drifted` matching the pre-test phase).

## Status transitions on the IOSXEConfig

A watcher on `kubectl get iosxeconfig cat9k-smoke -n cisco-vk-smoke -w` should observe at least one tick where `phase=LeaseBlocked`. Pre-Wave-8.2 the new pod's reconcile would have routed lease conflicts through the engine, producing either `Phase=Failed` (all-blocked → empty `ManagedFamilies` trips engine.validate) or `Phase=InSync` (partial-block masks missed families).

## Lease holders

Before the rotation:
- Lease holder identity: `cisco-vk-smoke/cat9k-smoke#<old-pod-uid>`

During the overlap:
- New pod attempts identity: `cisco-vk-smoke/cat9k-smoke#<new-pod-uid>`
- Different runtime suffix → cannot renew the old pod's lease (Wave 7A.3 closure)

After the overlap:
- Lease holder identity: `cisco-vk-smoke/cat9k-smoke#<new-pod-uid>`

Wave 7A.3's runtime-suffix is the architectural foundation; without it both pods would have the same identity (`cisco-vk-smoke/cat9k-smoke`) and could both successfully renew the lease, opening a duplicate-writer window.

## Requeue interval

The controller-runtime path's `RequeueAfter` for the LeaseBlocked tick should be **15s**, not 5m. Wave 9.2 made this read from the post-reconcile `engine.Result.Phase` rather than the stale pre-update `cr.Status.Phase`; without that fix, the requeue would have used the spec's `driftDetectInterval` (5m default) and the contention window would have lasted minutes instead of seconds.

The exact interval is observable via `kubectl events` if the cisco-vk pod emits requeue events at debug level, or by stopwatch on consecutive reconcile log lines.

## Device-side state

**Identical throughout.** The credential rotation is a Secret-only event; no `Fetch` returns drift, no `Mutate` is issued. The device sees exactly two new RESTCONF login sessions during the overlap (one per pod), nothing else.

## What would fail

- `Phase` never transitions to `LeaseBlocked` → the new pod isn't seeing the lease conflict, suggesting the runtime-suffix isn't applied (Wave 7A.3 regression).
- `Phase=Failed` instead of `LeaseBlocked` → the engine ran with empty `ManagedFamilies` (Wave 8.2 regression).
- `RequeueAfter` is 5m → the requeue read the stale CR phase (Wave 9.2 regression).
- Device-side configuration changed → a duplicate-writer window opened (lease arbitration regression).
