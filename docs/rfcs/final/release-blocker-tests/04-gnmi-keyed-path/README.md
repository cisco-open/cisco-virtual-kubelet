# Test 04 — gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` keyed-path

**§6.D.ii row:** "gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` → keyed-path PathSpec preserved verbatim on the wire."
**Closing waves:** 5A-fu and 7B ([`../../../external-review-followup-response.md`](../../../external-review-followup-response.md), [`../../../external-review-next-actions-response.md`](../../../external-review-next-actions-response.md)).

## What this test proves

Wave 5A-fu added structured `transport.Op.PathSpec []PathElement` so writers populate the gNMI keyed-path keys directly on every keyed-list op rather than encoding them as a string. Wave 7B added `PathSpec` to the handwritten `interface_ethernet`, `interface_switchport`, and `nestedKeyedListWriter` writers.

The pre-fix path was: writer emits a string `Path` like `/interface[type=GigabitEthernet][name=0/0/0]/description`, the gNMI transport parses it back into `PathElement{}` for the `gnmi.Path` proto. The parse step lost the `0/0/0` slashes because the keyed-list value contained the same delimiter the path used.

This test proves the structured `PathSpec` survives end-to-end: the device-side `description` lands on `GigabitEthernet0/0/0`, not on a malformed path that silently no-ops or hits a different interface.

## Device surface used

**One physical interface.** The test writes a `description` string to a single ethernet interface — the only field touched is `description`, which is a free-form annotation with no operational effect. The interface is **not** brought up/down, no IP, VRF, ACL, or speed/duplex change.

**Choosing the interface.** The runbook defaults to `GigabitEthernet0/0/0` because that is the path the closing wave specifically tested. **Confirm with the operator that 0/0/0 is not in production use** before applying — if it is, edit `00-apply.yaml` to use a known-spare port (e.g. `GigabitEthernet1/0/24` if that port is dark in your lab). The post-test rollback removes the description, restoring the pre-test state.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Captures the current `description` field on the chosen interface (likely empty or the operator's existing label). Anything else on the interface is read-only context.

## Apply

```sh
kubectl apply -f 00-apply.yaml
```

Creates `IOSXEConfig test-04-gnmi-keyed-path` with `managedFamilies: [interface_ethernet]`, `driftPolicy: revert`, and an inline source that sets `description: "cisco-vk release-blocker test 04 — wave 5A-fu/7B"` on the chosen interface.

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == InSync`
- The chosen interface's `description` on the device equals the test string (verified via direct RESTCONF GET).
- gNMI Set wire-trace (if captured) shows the `PathElement` for `interface[type=GigabitEthernet][name=0/0/0]/description` with `0/0/0` preserved verbatim — not URL-encoded, not split, not interpreted as additional path segments.

## Verify

```sh
bash ./verify.sh
```

## Rollback

```sh
bash ./rollback.sh
```

Deletes the test CR; the cisco-vk pod's deletion finalizer runs an empty-intent reconcile against the family, which removes the description (interface returns to pre-state).
