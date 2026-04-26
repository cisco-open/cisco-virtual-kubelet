# Test 01 — Expected outcome

## Phase + family status

```yaml
status:
  phase: InSync
  observedGeneration: 1
  familyStatus:
    - name: interface_loopback
      state: InSync
      opCount: 1   # the Replace/Merge for Loopback9999
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
```

## Device-side state

`Loopback9999` exists with the test description and IP. RESTCONF check:

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9999"
```

Should return `200 OK` with a JSON body containing:

```json
{
  "Cisco-IOS-XE-native:Loopback": {
    "name": "9999",
    "description": "cisco-vk release-blocker test 01 — wave 1A-fu",
    "ip": {
      "address": {
        "primary": {
          "address": "10.255.255.99",
          "mask": "255.255.255.255"
        }
      }
    }
  }
}
```

## Transactional lifecycle

The cisco-vk pod's debug log should show the following call sequence, in order, all attributed to the same `txid`:

1. `StartTransaction()` → returns a non-zero `TxHandle` (e.g. NETCONF session/edit-config-target).
2. `Mutate(tx, [<edit-config for Loopback9999>])` → success.
3. `Fetch(tx, ...)` (verify phase) → returns the candidate-datastore's Loopback list **including** `Loopback9999`. **Pre-Wave-1A-fu this would read from running and miss the in-flight write.**
4. `Commit(tx)` → success.
5. `Phase=InSync` recorded.

If `Discard(tx)` is logged anywhere, the test failed — the engine rolled back. Capture the failure cause from `kubectl describe iosxeconfig`.

## Other Loopbacks

**Untouched.** The `managedFamilies` list contains only `interface_loopback`, but the per-CR ManagedFamilies semantics ensure existing Loopbacks NOT in this CR's intent are left alone (additive day-0 reconcile, since `pruneOnRelinquish=false` is the default). Confirm via the post-state diff that no other Loopback's description or IP changed.
