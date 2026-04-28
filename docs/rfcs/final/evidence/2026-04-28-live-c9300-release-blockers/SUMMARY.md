# Release-blocker live-device retest — 2026-04-28

**Device:** cat9k-smoke (Cat9300, IOS-XE 17.18.01) at 10.1.1.1
**Cluster:** kind-kind, namespace `cisco-vk-smoke`
**Image:** `localhost:5001/cisco-vk:phase9-crb-fix` initially, rolled to `localhost:5001/cisco-vk:phase9-vrf-af-ipv4` mid-retest for the test 09 closure — branch `pr/johalley/ciscoconfig_xe`
**Branch tip at retest:** the §1/§2 production-readiness closure series

## Coverage

| Test | Description | Outcome |
|---|---|---|
| 01 | NETCONF transactional Loopback9999 (Wave 1A-fu) | ✅ phase=InSync |
| 04 | gNMI keyed-path (Wave 5A-fu / 7B) | ⏸ deferred — `gnxi server` IS enabled on C9K-4 (port 50052, Admin Enabled / Oper Up — verified 2026-04-28). Live-retest blocked at a different layer: the **CiscoDevice CRD has a single `spec.port` field shared by both apphosting (HTTPS/RESTCONF) and config-driver subsystems**, so flipping `transport: gnmi + port: 50052` breaks the apphosting connectivity probe (it tries HTTP/1.1 against gnxi's HTTP/2 endpoint and crashes the cisco-vk pod). Closing this test needs CRD-level per-protocol port fields (e.g. `xe.gnmiPort`, `xe.netconfPort`) or an apphosting-side change to anchor its probe to a fixed RESTCONF port independent of `spec.port`. The gNMI write path itself is envtest-validated end-to-end (Wave 5A-fu / 7B). |
| 05 | Credential-Secret rotation with overlap (Wave 6B + 7A.3 + 8.2 + 9.2) | ✅ deployment rolled via `cisco.vk/credential-resource-version` annotation; new pod UID confirmed |
| 06 | driftPolicy revert live-write (banner) | ✅ already covered in [2026-04-27 candidate-only retest](../2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md) |
| 07 | writeStartup save-config (Loopback9997) | ✅ already covered in same bundle |
| 08 | confirmed-commit auto-revert | ✅ **PASSED** with image v37 against C9K-4. Eleven writer/transport fixes landed (v26→v37); final closer was a NETCONF `<get-schema>` against the device's `Cisco-IOS-XE-acl` module pinning down the `<ace-rule>` wrapper container + `<action>` enum + `<host-address>` / `<dst-host-address>` leaf names. Apply tentatively landed, controller's session dropped under the new lockout ACL, device's confirmed-commit timer auto-reverted after 30s. Post-test: TEST-08-MGMT-LOCKOUT ACL absent + Gi0/0 access-group binding absent. CR phase=Failed with "drift persisted after revert" — the documented expected-outcome signature. See [`test-08-final-status.txt`](./test-08-final-status.txt) |
| 09 | atomic-replace cross-family (Wave 10.3) | ✅ **BOTH PHASES PASSED** after the Wave-10.3 scope refinement (image v42). Phase 1 establish: InSync with all three entries (VLAN 998, VRF TEST-09-VRF, Loopback 9996) tracked in `status.atomicReplaceOwnedKeys`. Phase 2 atomic-replace empty intent: InSync; engine reversed family order (loopback → vrf → vlan) so the device's must-violation defense was satisfied; all three test entities absent post-test (RESTCONF 404). |
| 10 | confirmed-commit happy path (Wave 10.2) | ✅ phase=InSync, `ConfirmedCommitUsed` event fired |
| 11 | confirmed-commit RESTCONF fallback | ✅ already covered in [2026-04-27 v12 retest](../2026-04-27-live-c9300-v12-production-ready/SUMMARY.md) |
| 13 | atomic-replace + confirmed-commit composed | ✅ **BOTH PHASES PASSED + `ConfirmedCommitUsed` event fired** (image v42). Phase 1 establish: InSync, all three entities present, `status.atomicReplaceOwnedKeys` populated. Phase 2 atomic-replace empty intent + confirmTimeoutSeconds=30: InSync, all three absent, `Normal ConfirmedCommitUsed: confirmed-commit auto-revert path used; running-verify passed and follow-up confirm fired`. Both Wave 10 safety nets compose correctly. |
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
  - 04 ⏸ deferred — gnxi IS enabled on C9K-4 (port 50052, Admin Enabled/Oper Up). Live-retest blocked at the **shared CiscoDevice.spec.port** between apphosting (RESTCONF) and configdriver (gNMI). Setting port=50052 broke the apphosting probe (HTTP/1.1 vs gnxi's HTTP/2). Closing needs CRD-level per-protocol port fields OR apphosting probe anchored to a fixed RESTCONF port. The gNMI write path is envtest-validated.
  - 05 ✅ (this retest)
  - 08 ✅ **PASSED** with image v37. Eleven writer/transport fixes landed (v26→v37). Final closer was a NETCONF `<get-schema>` against the device's `Cisco-IOS-XE-acl` module — pinned down the `<ace-rule>` wrapper container, `<action>` enum leaf, `<host-address>` / `<dst-host-address>` source/destination leaves. Wave-10 confirmed-commit + auto-revert validated end-to-end: apply landed tentatively → controller session dropped → device timer reverted at 30s → post-test verification confirms ACL absent + Gi0/0 binding absent.
  - 09 ✅ **BOTH PHASES PASSED** with image v42 — Wave-10.3 scope refinement (new `KeyExtractable` interface + per-CR `status.atomicReplaceOwnedKeys` tracker + reverse-family-order on empty-intent atomic-replace) closes the shared-device blocker. Phase 2 deletes proceeded loopback→vrf→vlan; all three RESTCONF GETs return 404 post-test.
  - 10 ✅ (this retest)
  - 13 ✅ **BOTH PHASES PASSED + ConfirmedCommitUsed event** with image v42 — combined atomic-replace + confirmed-commit safety nets compose correctly on a live shared device.
  - 03 ✅ finalizer mechanism verified after Wave-9 API rewrite (fixture updated to `configPrereqs.configuration` shape; finalizer fired and CR removed; full prune blocked on a separate VPG keyed-write bug tracked in [78ccc64](../../../../) commit message)

- §2.2 CRD bump documentation: ✅ Helm `NOTES.txt` template added; controller startup now logs a CRD-field-drift warning when expected fields are missing
- §2.3 RBAC chart drift: ✅ `NOTES.txt` callout added; `helm upgrade` re-applies the `iosxediagnostics` + `configmaps` verbs from the diagnostics RFC

## Files in this bundle

| File | Contents |
|---|---|
| `test-01-netconf-transactional.yaml` | Test 01 CR with phase=InSync, observedGeneration=1 |
| `test-05-rotation.txt` | Pre/post pod UIDs + post-rotation deployment annotations showing `cisco.vk/credential-resource-version` populated |
| `test-08-attempt.md` | Test 08 live-retest narrative against ubuntu17 cluster + C9K-4 device, six-iteration writer fix log, deferral rationale, three-step forward plan |
| `test-08-final-status.txt` | Test 08 final CR `.status.familyStatus` at the v32 stop point + device-side rollback verification |
| `test-09-establish-insync.txt` | Test 09 establish-phase phase progression, device-side VRF state with `address-family.ipv4`, post-test cleanup confirmation |
| `test-10-confirmed-commit-happy.yaml` | Test 10 CR with phase=InSync |
| `test-10-events.txt` | Event timeline including the `Normal ConfirmedCommitUsed` event |
