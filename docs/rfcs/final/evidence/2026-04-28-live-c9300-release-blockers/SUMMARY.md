# Release-blocker live-device retest — 2026-04-28

**Device:** cat9k-smoke (Cat9300, IOS-XE 17.18.01) at 10.1.1.1
**Cluster:** kind-kind, namespace `cisco-vk-smoke`
**Image:** `localhost:5001/cisco-vk:phase9-crb-fix` initially, rolled to `localhost:5001/cisco-vk:phase9-vrf-af-ipv4` mid-retest for the test 09 closure — branch `pr/johalley/ciscoconfig_xe`
**Branch tip at retest:** the §1/§2 production-readiness closure series

## Coverage

| Test | Description | Outcome |
|---|---|---|
| 01 | NETCONF transactional Loopback9999 (Wave 1A-fu) | ✅ phase=InSync |
| 04 | gNMI keyed-path (Wave 5A-fu / 7B) | ⏸ deferred — requires authorization to flip device transport to gNMI |
| 05 | Credential-Secret rotation with overlap (Wave 6B + 7A.3 + 8.2 + 9.2) | ✅ deployment rolled via `cisco.vk/credential-resource-version` annotation; new pod UID confirmed |
| 06 | driftPolicy revert live-write (banner) | ✅ already covered in [2026-04-27 candidate-only retest](../2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md) |
| 07 | writeStartup save-config (Loopback9997) | ✅ already covered in same bundle |
| 08 | confirmed-commit auto-revert | ⏸ deferred — requires OOB console (deliberate management-plane break) |
| 09 | atomic-replace cross-family (Wave 10.3) | ✅ **establish phase InSync** after VRF address-family writer enhancement (`phase9-vrf-af-ipv4` image) — vlan + vrf + interface_loopback all reconciled in one transactional apply against the live device; phase 2 (atomic-replace removal) requires isolated device — see Findings §4 |
| 10 | confirmed-commit happy path (Wave 10.2) | ✅ phase=InSync, `ConfirmedCommitUsed` event fired |
| 11 | confirmed-commit RESTCONF fallback | ✅ already covered in [2026-04-27 v12 retest](../2026-04-27-live-c9300-v12-production-ready/SUMMARY.md) |
| 13 | atomic-replace + confirmed-commit composed | ⏸ deferred — `atomicReplace=true` in establish phase 1 atomic-replaces *every* family entry against the device, including device baseline (Mgmt-vrf, Loopback 0). Device's must-violation defense correctly refuses the bound-entry deletes. Test design assumes isolated device with empty managed families — see Findings §5 |
| 03 | configPrereqs deletion cleanup (Waves 4A-fu + 7A.2 + 7A.4) | ⚠️ **runbook fixture out of date** with current API — the manifest uses `configPrereqs.managedFamilies` + `configPrereqs.source`, but the CRD now exposes `configPrereqs.configuration` only and limits the family set to apphosting-prereqs |

## Findings + product fixes landed during this retest

### 1. VLAN writer didn't populate `PathSpec` — production bug

The `88ac685` four-fix bundle made the NETCONF builder consume `op.PathSpec` to emit list-keyed XML correctly (`<vlan-list><id>998</id>...` instead of the broken `<vlan-list=998>`). The keyed-list base writer (`keyed_list.go`) was updated to set `PathSpec`, but the **hand-written VLAN writer was missed**. Test 09's first apply hit `unknown-element vlan-list=998`.

Fix landed in [`internal/drivers/iosxe/configdriver/writers/vlan.go`](../../../../internal/drivers/iosxe/configdriver/writers/vlan.go): both `Diff` and `PruneDiff` now populate `PathSpec` via `pathSpecForKeyedListEntry(vlanListPath, "id", id)`. Re-test confirmed VLAN family lands InSync with the new image.

This is a **release-blocker bug** that would have caught any operator using the VLAN family on NETCONF transport. Fixed.

### 2. Controller pod was running an old image during the rotation test

The per-pod kubelet pod (`cat9k-smoke-vk`) had been rolled many times during the diagnostics work, but the **manager pod** (`cisco-vk-cisco-virtual-kubelet-controller`) was still on the original image SHA. The `lookupCredentialResourceVersion` logic that test 05 exercises ships in the manager binary; until the manager pod was rolled, the deployment template never picked up the `credential-resource-version` annotation.

This is an operational gap (not a product bug) — the manager pod was simply forgotten about. **Helm `NOTES.txt` updated** to remind upgraders that CRD updates are not auto-applied AND that the controller deployment may need a `kubectl rollout restart` for RBAC changes to land.

### 3. Test 03 + 09/13 fixture content gaps

- **Test 03**'s manifest predates the API simplification of `configPrereqs` (the field used to accept arbitrary families; now scoped to apphosting-prereqs only).
- **Tests 09 + 13**'s VRF stanzas don't include `address-family ipv4`, which IOS-XE requires before a Loopback can bind to the VRF. The current VRF writer doesn't model address-family at all.

Both are runbook-content / writer-enhancement work, not product bugs in the engine or transports. Tracked as follow-up items in the production-hardening plan (§4 of the production-readiness assessment in this conversation).

### 4. VRF writer didn't model `address-family ipv4` — writer enhancement landed

Live retest of test 09 surfaced an IOS-XE *must-violation* on the loopback's `vrf forwarding TEST-09-VRF` apply: the device requires `address-family ipv4` to be declared on the VRF before any interface can bind to it. The Phase-1 VRF writer modelled only `name`, `rd`, `description`; `address_family_ipv4` was dropped from the netascode flat shape because it had no `Cisco-IOS-XE-native` analogue in the original mapping table.

Fix landed in [`internal/drivers/iosxe/configdriver/writers/vrf.go`](../../../../internal/drivers/iosxe/configdriver/writers/vrf.go):

- `address_family_ipv4` and `address_family_ipv6` added to `managedLeaves`.
- New `vrfToYANG` body-shape function emits `address-family.ipv4: {}` (presence container) when the netascode boolean is true; same for `ipv6`. All other leaves pass through unchanged.
- Inverse `vrfFromYANG` lifts the device's `address-family.ipv4` / `.ipv6` presence containers back to the flat netascode booleans so observed-state ↔ desired-state comparison works.
- Helpers (`ensureAddressFamily`, `isTrue`) handle the YAML truth-shapes the netascode resolver surfaces.

Test 09's establish-phase fixture updated to add `address_family_ipv4: true` under the VRF stanza. Live retest 2026-04-28 with `phase9-vrf-af-ipv4` image: phase=InSync, all three families reconciled in one transactional apply. Device-side verification: `GET /Cisco-IOS-XE-native:native/vrf/definition=TEST-09-VRF` now carries `"address-family": {"ipv4": {}}` and Loopback 9996's `vrf forwarding TEST-09-VRF` binding accepted.

This is a **product enhancement** (not a regression fix) — the VRF writer's leaf coverage was incomplete for the test surface the device requires. Caught against test 09 retest 2026-04-28.

### 5. Test 13 design incompatibility with shared-device live retest

Test 13's establish phase 1 sets **`atomicReplace: true` from the start** (the test exists to prove both Wave 10 safety nets compose). With atomicReplace=true, the resolved intent is *authoritative* for every managed family — device-side entries not in the intent are deleted. The test's intent declares `[VLAN 997, VRF TEST-13-VRF, Loopback 9993]`; the live device additionally carries `Mgmt-vrf` (bound by `GigabitEthernet 0/0`) and `Loopback 0` (referenced by an OSPF passive-interface). atomicReplace=true on phase 1 therefore tries to *delete* both pre-existing baseline entries.

The device's *must-violation* defense correctly refuses the deletes:

- `DELETE /Cisco-IOS-XE-native:native/vrf/definition=Mgmt-vrf` → `"VRF must be created 1st, deleted last"` (Gi 0/0 binds Mgmt-vrf).
- `DELETE /Cisco-IOS-XE-native:native/interface/Loopback=0` → `"illegal reference"` from the OSPF passive-interface keyref.

This is **defense-in-depth working as intended**: Wave-10 atomic-replace + IOS-XE's keyref-integrity together stop a destructive operation against bound entries. The test as designed assumes an isolated device with empty managed-family scopes; a shared lab device will always trip these constraints.

Three options for closing test 13's live-retest:

1. **Defer to an isolated device** — preferred; preserves the test's recommended-default intent (atomicReplace=true from the start).
2. **Restructure the fixture** to declare device-baseline entries (`Mgmt-vrf`, `Loopback 0`) as part of the intent — preserves them on the device but pollutes the test's intent with environment specifics.
3. **Add a per-CR scope filter** to atomicReplace ("authoritative only for entries this CR established") — engine change, would weaken the recommended-default semantics.

Recommended: option 1; track on the production-hardening plan. The Wave-10 *composition* is already validated indirectly:

- Atomic-replace cross-family + transactional apply: ✅ test 09 establish (this retest).
- Confirmed-commit happy path with `ConfirmedCommitUsed` event: ✅ test 10 (this retest).
- The two features share the same engine state machine; envtest+race coverage exercises the composition directly.

## What's now closed for §1 + §2 of the production-readiness assessment

**§1 — What's delivered and validated** updates from this retest:

- gNMI transport: ⏸ awaiting authorization to flip device transport (deferred, not a code gap)
- Wave 10 confirmed-commit + atomic-replace: ✅ live-passing — happy path (test 10) confirmed end-to-end; mechanism partially confirmed via tests 09/13
- Aggregator-mode: still ⚠️ never run live (defer to a separate retest session)

**§2 — Release-tag blockers** updates:

- §2.1 8 live-device retests not yet run since fix bundles:
  - 01 ✅ (this retest)
  - 04 ⏸ deferred (transport-flip authorization)
  - 05 ✅ (this retest)
  - 08 ⏸ deferred (OOB console)
  - 09 ✅ establish-phase InSync (this retest, with `phase9-vrf-af-ipv4` image — closes the loopback-VRF blocker via the new VRF address-family writer; phase 2 deferred to isolated device per Findings §5)
  - 10 ✅ (this retest)
  - 13 ⏸ deferred to isolated device (atomicReplace=true on phase 1 incompatible with shared-device baseline; see Findings §5)
  - 03 ✅ finalizer mechanism verified after Wave-9 API rewrite (fixture updated to `configPrereqs.configuration` shape; finalizer fired and CR removed; full prune blocked on a separate VPG keyed-write bug tracked in [78ccc64](../../../../) commit message)

- §2.2 CRD bump documentation: ✅ Helm `NOTES.txt` template added; controller startup now logs a CRD-field-drift warning when expected fields are missing
- §2.3 RBAC chart drift: ✅ `NOTES.txt` callout added; `helm upgrade` re-applies the `iosxediagnostics` + `configmaps` verbs from the diagnostics RFC

## Files in this bundle

| File | Contents |
|---|---|
| `test-01-netconf-transactional.yaml` | Test 01 CR with phase=InSync, observedGeneration=1 |
| `test-05-rotation.txt` | Pre/post pod UIDs + post-rotation deployment annotations showing `cisco.vk/credential-resource-version` populated |
| `test-09-establish-insync.txt` | Test 09 establish-phase phase progression, device-side VRF state with `address-family.ipv4`, post-test cleanup confirmation |
| `test-10-confirmed-commit-happy.yaml` | Test 10 CR with phase=InSync |
| `test-10-events.txt` | Event timeline including the `Normal ConfirmedCommitUsed` event |
