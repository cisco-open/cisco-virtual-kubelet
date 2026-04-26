# Test 05 — Credential Secret rotation with overlap window

**§6.D.ii row:** "Credential Secret rotation with overlap window → new pod takes the lease cleanly, transient `LeaseBlocked` with sub-TTL requeue, no concurrent writes."
**Closing waves:** 6B + 7A.3 + 8.2 + 9.2.

## What this test proves

A real CiscoDevice operator's credentials rotate periodically. When the Secret's `password` value changes, the controller must:

1. Roll the per-device cisco-vk Deployment (via the `cisco.vk/credential-resource-version` annotation on the pod template, Wave 6B) so the new pod picks up the new password.
2. During the brief overlap window where both the old and new pods exist, the runtime-suffixed lease identity (Wave 7A.3) keeps them from concurrently renewing the same `(device, family)` lease.
3. The new pod's first reconcile observes the lease still held by the old pod → reports `Phase=LeaseBlocked` (Wave 8.2).
4. The controller-runtime path requeues at the sub-TTL interval (15s, Wave 9.2) rather than the normal 5m drift interval, so the lease handover resolves quickly.

This is the **only release-blocker test that does not modify device-side state.** It exercises pod-side and lease-layer behaviour. The device-side configuration is untouched throughout.

## Device surface used

**None.** No RESTCONF/NETCONF/gNMI writes occur. Read-only Fetch calls happen as part of normal reconciliation, but no Mutate.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Captures the current cisco-vk pod identity (UID + name), the current `cisco.vk/credential-resource-version` annotation on the pod template, and the current per-(device, family) Lease holder identities. After rollover, the pod UID changes, the annotation changes, and the lease holders briefly overlap before settling on the new pod's runtime ID.

## Apply

```sh
kubectl apply -f 00-apply.yaml
```

This is **not** a new IOSXEConfig — it is a Secret update that simulates a credential rotation. The manifest patches the existing `cat9k-creds` Secret's `password` field with the same value but a different formatting (e.g. trailing whitespace stripped, or a new line appended), forcing the Secret's `metadata.resourceVersion` to bump. The actual password value MUST remain valid; otherwise the new pod will fail to authenticate to the device and the test result is meaningless.

**Important:** `00-apply.yaml` cannot include the actual password value. Before applying, edit it to inject the current password into the `stringData.password` field, OR run the alternative `kubectl patch` command in the runbook that preserves the existing password while bumping the resourceVersion.

## Expected

See [`expected.md`](./expected.md). Summary:

- Within ~5s of the Secret update, the controller stamps a new value on the per-device Deployment's pod-template `cisco.vk/credential-resource-version` annotation.
- The Deployment rolls: old pod terminates, new pod starts.
- The new pod's first reconcile of any `IOSXEConfig` shows `status.phase == LeaseBlocked` for the brief overlap window.
- The next requeue happens within 30s (sub-TTL), not 5m (drift interval).
- Eventually `status.phase` returns to whatever its pre-test value was (likely `InSync` or `Drifted`).
- No device-side change occurred at any point.

## Verify

```sh
bash ./verify.sh
```

`verify.sh` runs a watcher on the `IOSXEConfig` status and asserts:
- Phase transitioned through `LeaseBlocked` (visible at least once).
- No `cisco_vk_engine_apply_ops_total` increment during the overlap.
- New pod is `Ready` within 60s.

## Rollback

```sh
bash ./rollback.sh
```

Reverts the Secret to its pre-rotation form (which, since the password value is unchanged, just removes the cosmetic edit). The pod will roll once more to pick up the "new" (actually pre-test) annotation value. End state matches pre-test exactly.
