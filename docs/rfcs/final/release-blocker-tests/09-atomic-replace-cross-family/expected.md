# Test 09 — Expected outcome

## Phase 1 (establish state)

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

Device-side: VLAN 998, VRF `TEST-09-VRF`, Loopback 9996 all present. Loopback 9996's `vrf forwarding TEST-09-VRF` is set.

## Phase 2 (atomic remove)

After `kubectl apply -f 01-apply-empty-with-atomic.yaml`:

```yaml
status:
  phase: InSync
  observedGeneration: 2
  familyStatus:
    - name: vlan
      state: InSync          # empty intent matched empty device state for this family
      opCount: 1             # one delete op (VLAN 998)
    - name: vrf
      state: InSync
      opCount: 1             # one delete op (TEST-09-VRF)
    - name: interface_loopback
      state: InSync
      opCount: 1             # one delete op (Loopback 9996)
```

Device-side: all three test entities absent. Verify via three RESTCONF GETs, each expected to return 204/404.

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --output /dev/null --write-out '%{http_code}\n' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/vlan/vlan-list=998"
# expected: 204 or 404

curl --silent --insecure --user "${USER}:${PASS}" \
  --output /dev/null --write-out '%{http_code}\n' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/vrf/definition=TEST-09-VRF"
# expected: 204 or 404

curl --silent --insecure --user "${USER}:${PASS}" \
  --output /dev/null --write-out '%{http_code}\n' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9996"
# expected: 204 or 404
```

## Single-transaction invariant

The deletes ran as ONE transaction. The cisco-vk pod's debug log should show the call sequence (one `<lock>`, multiple `<edit-config>`, one `<commit>`, one `<unlock>`) with the same TxHandle threaded through every Mutate. There must NOT be:

- Two separate `<commit>` calls (would mean two transactions; the atomic invariant is broken).
- A `<commit>` followed by additional `<edit-config>` (same point — the second `<edit-config>` would land in a separate transaction).

## Cross-family ordering

Per the schema's `depends_on` declarations, `interface_loopback` declares no explicit dependency, but `interface_ethernet` does (`depends_on: [vrf]`). For atomic replace cleanup, the engine's topo-sort orders families as:

```
[vrf, vlan, interface_loopback]      # adds — parents first
```

Reversed for deletes (parents last) would be:

```
[interface_loopback, vrf, vlan]
```

The current implementation uses the topo-sort for forward order. The atomic-replace removal flow within a single transaction should emit the loopback's VRF unbinding BEFORE removing the VRF — but because Diff produces one set of ops per family, the writer's per-family delete-set is what enforces the wire-level order within each `<edit-config>`. Cross-family ordering is the cisco-vk-config-lint concern; this test asserts that the ENGINE delivered a coherent end state, not the per-RPC ordering.

## What would fail

| Failure | Likely cause |
|---|---|
| One of the three test entities still on the device after phase 2 | atomicReplace is not driving PruneCapable.PruneDiff for that family. Check the engine's `if res.AtomicReplace { res.PruneOnRelinquish = true }` boundary mutation. |
| All three deletes happened but as separate transactions | The engine fell back to the non-atomic path. Check `Result.ConfirmedCommitFallback` — there shouldn't be one for this test (test 09 doesn't use confirmed-commit) but the check is informative. |
| Phase=Failed with "drift persisted after revert" | The atomic-replace path emitted ops that the device rejected. Pod logs will show which family's Apply or Verify failed. |
| Loopback's vrf-binding still present after phase 2 (just the VRF gone, the loopback's reference orphan) | Per-family delete-set in the loopback writer doesn't include the vrf-binding teardown. This is a writer-level bug, not engine-level. |
