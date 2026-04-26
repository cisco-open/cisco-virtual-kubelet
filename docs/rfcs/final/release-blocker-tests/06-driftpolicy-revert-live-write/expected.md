# Test 06 — Expected outcome

## Phase 1 (driftPolicy: report)

```yaml
status:
  phase: Drifted
  observedGeneration: 1
  familyStatus:
    - name: banner
      state: Drifted
      message: "drift detected: motd"
  drift:
    - family: banner
      path: /Cisco-IOS-XE-native:native/banner/motd/banner
      desired: "cisco-vk release-blocker test 06 — wave drift-detect"
      observed: <pre-test banner content, possibly empty>
  conditions:
    - type: Ready
      status: "False"
      reason: Drifted
```

Device-side: **banner is UNCHANGED.** The `report` policy never writes.

## Phase 2 (driftPolicy: revert)

```yaml
status:
  phase: InSync
  observedGeneration: 2     # generation bumped by the spec.driftPolicy change
  familyStatus:
    - name: banner
      state: InSync
      opCount: 1
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
```

Device-side: `banner motd` now reads:

```
cisco-vk release-blocker test 06 — wave drift-detect
```

RESTCONF check:

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/banner/motd"
```

## Other system-level fields

**Untouched.** Only the `banner` family is in `managedFamilies`; the engine should not have written anything outside it. Verify by spot-checking hostname, domain-name, ip-http server state, ntp servers, snmp config — all should equal pre-state.

## Counters

- `cisco_vk_engine_reconciles_total{phase="Drifted"}` increments at least once during phase 1.
- `cisco_vk_engine_reconciles_total{phase="InSync"}` increments after phase 2's apply.
- `cisco_vk_engine_apply_ops_total{family="banner"}` increments by 1 in phase 2 only.
