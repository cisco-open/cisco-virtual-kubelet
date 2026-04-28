# Release-blocker live-device retest — 2026-04-28

**Device:** cat9k-smoke (Cat9300, IOS-XE 17.18.01) at 10.1.1.1
**Cluster:** kind-kind, namespace `cisco-vk-smoke`
**Image:** `localhost:5001/cisco-vk:phase9-crb-fix` — branch `pr/johalley/ciscoconfig_xe`
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
| 09 | atomic-replace cross-family (Wave 10.3) | ⚠️ **partial** — atomic-replace mechanism validated (VLAN ✅ + VRF ✅ in same transaction); loopback-VRF binding blocked on a separate VRF writer enhancement (see §"Findings" below) |
| 10 | confirmed-commit happy path (Wave 10.2) | ✅ phase=InSync, `ConfirmedCommitUsed` event fired |
| 11 | confirmed-commit RESTCONF fallback | ✅ already covered in [2026-04-27 v12 retest](../2026-04-27-live-c9300-v12-production-ready/SUMMARY.md) |
| 13 | atomic-replace + confirmed-commit composed | ⚠️ same loopback-VRF blocker as test 09 |
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
  - 09 ⚠️ partial (mechanism validated, fixture gap on loopback-VRF)
  - 10 ✅ (this retest)
  - 13 ⚠️ partial (same fixture gap as test 09)
  - 03 ⚠️ fixture out of date (no product issue)

- §2.2 CRD bump documentation: ✅ Helm `NOTES.txt` template added; controller startup now logs a CRD-field-drift warning when expected fields are missing
- §2.3 RBAC chart drift: ✅ `NOTES.txt` callout added; `helm upgrade` re-applies the `iosxediagnostics` + `configmaps` verbs from the diagnostics RFC

## Files in this bundle

| File | Contents |
|---|---|
| `test-01-netconf-transactional.yaml` | Test 01 CR with phase=InSync, observedGeneration=1 |
| `test-05-rotation.txt` | Pre/post pod UIDs + post-rotation deployment annotations showing `cisco.vk/credential-resource-version` populated |
| `test-10-confirmed-commit-happy.yaml` | Test 10 CR with phase=InSync |
| `test-10-events.txt` | Event timeline including the `Normal ConfirmedCommitUsed` event |
