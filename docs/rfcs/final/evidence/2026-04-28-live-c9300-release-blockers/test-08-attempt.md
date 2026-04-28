# Test 08 — confirmed-commit auto-revert (live-retest attempt 2026-04-28)

**Device:** Cat9300 / IOS-XE 17.18.2, mgmt interface GigabitEthernet0/0 in Mgmt-vrf, IP 198.51.100.103.
**Cluster:** ubuntu17 k3s, namespace `cisco-vk-smoke`, controller `cvk-cisco-virtual-kubelet-controller`.
**Image series:** `containers.dmz.cisco.com:5000/pr/johalley/ciscoconfig_xe:v26 → v32` (six writer-level fixes during the retest; see commit log).

## What the test does

The most invasive entry on the release-blocker dashboard. Submits an
inbound deny-ACL on the management interface targeting the controller's
egress IP, with `spec.transactional: true` and
`spec.confirmTimeoutSeconds: 30`. Expected:

1. Transaction fires `<commit><confirmed/><confirm-timeout>30</confirm-timeout></commit>` against candidate.
2. Candidate applies tentatively to running. The new ACL filters the controller's RESTCONF/NETCONF source IP, so the controller's session drops mid-confirm.
3. Engine's `runningVerify` cannot Fetch (session is dead) → declines `ConfirmCommit`.
4. After 30s the device's own confirm-timeout timer reverts running to the pre-commit state. CR ends in `phase=Failed` with an error message containing "auto-revert" and `cisco_vk_config_transactions_total{outcome="auto_reverted"}` increments by 1.

## Live retest outcome — DEFERRED

**Status:** ⏸ deferred. The Wave-10 confirmed-commit machinery itself is unblocked (the cluster + controller + transports were brought all the way through the test pipeline up to the device-side apply phase), but the apply never reached `<commit><confirmed/>` because the **Phase-1 `access_list_extended` writer's per-rule body translation is incomplete**. Each write attempt hit a fresh layer-rejection from IOS-XE, drove a writer fix, and surfaced the next layer:

| Iteration | Image | Failure surface | Fix landed |
|---|---|---|---|
| v26 baseline | `phase9-vrf-af-ipv4` | `interface_ethernet` writer drops `ip_access_group_in` from managedLeaves | v27 — add `ip_access_group_in` / `_out` leaves + `interfaceIPv4VRFToYANG` access-group lift |
| v27 | `v27` | `unknown-element <bad-element>GigabitEthernet=0</bad-element>` — netconf transport split path on `/` inside the interface key value | v28 — URL-encode key per RFC 8040 §3.5.3.1; add `encodeKeyValue` helper |
| v27 | `v27` | `Diff: access_list_extended: observed: entry missing "name"` — keyedListWriter aborts on system-default ACLs that don't fit the schema | v28 — skip observed entries missing the keyField |
| v28 | `v28` | Same `entry missing "name"` because `nestedKeyedListWriter` had its own observed-list parser missed by the v28 fix | v29 — extend lenient handling to nestedKeyedListWriter |
| v29 | `v29` | `unknown-element <bad-element>extended</bad-element>` — netconf transport doesn't xmlns-declare `extended` because the writer's path uses bare `extended` instead of `Cisco-IOS-XE-acl:extended` | v30 — qualify the path's last segment |
| v30 | `v30` | `unknown-element <bad-element>rules</bad-element>` — body emitted `<rules>` as a literal element, but IOS-XE-acl YANG has no intermediate `<rules>` container | v31 — add YANGInner wrap (initially with `<rules>` retained — wrong) |
| v31 | `v31` | Same | v32 — drop netascode leaf entirely, emit `<access-list-seq-rule>` directly under `<extended>` |
| v32 | `v32` | `unknown-element <bad-element>src_host</bad-element>` — per-rule netascode fields (`src_host`, `dst_any`, `protocol`) need a netascode → IOS-XE-acl YANG translator (analogous to `interfaceIPv4VRFToYANG` for interface fields). The writer doesn't have one. | **NOT FIXED — deferred to a Phase-1 ACL writer feature-completion PR** |

The v32-final blocker is genuinely a writer feature gap — the Phase-1 `access_list_extended` writer was wired up structurally (Diff/Apply/Fetch + nested-key handling) but the per-rule body translation that would turn netascode shorthand (`src_host: 10.0.0.1`, `dst_any: true`, `protocol: ip`) into the IOS-XE-acl YANG schema (`source-host`, `dest-any`, `protocol-type`, etc.) was never written. The five interface_* writers got their `interfaceIPv4VRFToYANG` body-shape; the ACL writer didn't get its analogue.

## What this proves about Wave 10

Test 08 cannot validate Wave 10 confirmed-commit auto-revert end-to-end **on this device** until the ACL writer's body translation lands, because the ACL apply never reaches the `<commit><confirmed/>` stage. Wave 10 itself is validated indirectly:

- ✅ **Test 10** (confirmed-commit happy path, this dashboard) — `ConfirmedCommitUsed` event fired against the live device when running-verify succeeded; the engine state machine + transport `ConfirmedCommitter` interface compose correctly.
- ✅ **Test 09** establish (this dashboard) — the same engine state machine drives a transactional cross-family apply (vlan + vrf + interface_loopback) end-to-end on the live device.
- ✅ **Engine-side envtest** — the auto-revert decline path is covered in unit + envtest with race detection. The decline triggers when running-verify fails, regardless of *why* it failed (session loss, capability missing, transport regression).

The auto-revert machinery is shippable by composition; what test 08 would have closed is the *combined* ACL-write + confirmed-commit live proof. That proof requires the ACL writer to be feature-complete first.

## Forward plan

Three writer enhancements would unblock test 08's full retest, listed in the order they should land:

1. **`interfaceIPv4VRFToYANG`-style ACL rule body translator** — netascode `src_host` / `src_any` / `dst_host` / `dst_any` / `protocol` / `action` fields lifted to the IOS-XE-acl YANG schema (`source`, `destination`, `protocol-type`, choice-statement action, ports + named protocols). Most of this is mechanical — the netascode portal docs the corpus and the netascode resolver in upstream code already does this for the apphosting subsystem. ETA: 1–2 engineer-days.
2. **YANGInner wrapping audit** for the other netascode → YANG inner-list mismatches (route-map's `entries`, OSPF's `network` list, BGP's `neighbor` list). The pattern is identical to access-list's, and v31's nestedKeyedListWriter fix already supports the lift; only audit the existing writers' `nestedYANGInner` settings. ETA: half a day.
3. **Operator-runnable retest** of test 08 once #1 lands. Same fixture, no further engine changes needed. ETA: half a day.

Estimated total: 3 engineer-days from the current state to a green test 08.

## Files in this attempt

- `test-08-attempt.md` (this file) — the attempt's narrative.
- `test-08-final-status.txt` — the final CR `.status.familyStatus` + final per-pod kubelet log excerpt at the v32 stop point.
