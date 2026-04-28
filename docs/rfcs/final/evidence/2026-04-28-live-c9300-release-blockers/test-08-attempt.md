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
| v32 | `v32` | `unknown-element <bad-element>src_host</bad-element>` — per-rule netascode fields (`src_host`, `dst_any`, `protocol`) need a netascode → IOS-XE-acl YANG translator (analogous to `interfaceIPv4VRFToYANG` for interface fields). The writer doesn't have one. | v33 — `aclRuleToYANG` translator + per-spec `BodyShape` hook on `nestedListSpec` + companion ACL-standard path namespace + body-shape wiring (commit `5738a37`) |
| v33 | `v33` | `unknown-element <bad-element>ace-rule-action</bad-element>` — the IOS-XE-acl model doesn't expose `ace-rule-action` as the action enum; the action choice is encoded differently. | v34 — encode action as empty-leaf `<permit/>` / `<deny/>` siblings of `<sequence>` (commit `8a4f17e`) |
| v34 | `v34` | `expected tag: sequence, got tag: deny` — YANG strict-order: list keys must come first under the parent. JSON→XML emitter iterated `map[string]any` with Go's randomised order. | v35 — `orderedMapKeys` puts `name` / `id` / `sequence` / `no` / `prefix` first, then alphabetical (commit `3827b70`) |
| v35 | `v35` | `unknown-element <bad-element>deny</bad-element>` at path `…/access-list-seq-rule[sequence='10']/deny` — the action choice in this YANG variant is wrapped in some intermediate container that the iteration trail hasn't located yet (`<ace-rule>` is one candidate; an enum leaf with a different name is another). | **NOT FIXED — deferred to a focused YANG-schema discovery session** |

The v35-final blocker needs **direct YANG schema interrogation** against this specific device's `Cisco-IOS-XE-acl` module — it's no longer productive to iterate via cluster build/deploy/apply cycles. The right path is a single session with the device using a NETCONF `<get-schema>` RPC (or RESTCONF `?fields` query against an existing ACL whose admin-side credentials grant read), then a one-shot translator update to match.

What v32→v35 *did* close — the Phase-1 access-list writer was wired up structurally but missed five non-trivial pieces of the netascode → IOS-XE-acl translation pipeline. All five are now in place:

1. ✅ Per-rule `BodyShape` hook on `nestedListSpec` so families with netascode shorthand can declare their YANG translator (`5738a37`).
2. ✅ `aclRuleToYANG` translator covering host / network / wildcard / port / log / remark netascode fields (`5738a37`, `8a4f17e`).
3. ✅ Companion fix for `access_list_standard` — same path-namespace prefix and translator wiring (`5738a37`).
4. ✅ Action choice encoded as empty-leaf siblings (`8a4f17e`) — turns out to be **partially right**: the *names* `permit` / `deny` are correct YANG identifiers in this model, but they live one level deeper than v34 emitted them. v35 confirmed the names by surfacing a position error rather than a name error.
5. ✅ YANG strict-order list-key-first emission (`3827b70`) — surfaces in any future YANG list write that has non-key fields, not just ACL rules.

## What this proves about Wave 10

Test 08 cannot validate Wave 10 confirmed-commit auto-revert end-to-end **on this device** until the ACL writer's body translation lands, because the ACL apply never reaches the `<commit><confirmed/>` stage. Wave 10 itself is validated indirectly:

- ✅ **Test 10** (confirmed-commit happy path, this dashboard) — `ConfirmedCommitUsed` event fired against the live device when running-verify succeeded; the engine state machine + transport `ConfirmedCommitter` interface compose correctly.
- ✅ **Test 09** establish (this dashboard) — the same engine state machine drives a transactional cross-family apply (vlan + vrf + interface_loopback) end-to-end on the live device.
- ✅ **Engine-side envtest** — the auto-revert decline path is covered in unit + envtest with race detection. The decline triggers when running-verify fails, regardless of *why* it failed (session loss, capability missing, transport regression).

The auto-revert machinery is shippable by composition; what test 08 would have closed is the *combined* ACL-write + confirmed-commit live proof. That proof requires the ACL writer to be feature-complete first.

## Forward plan

Two pieces of work close test 08, in order:

1. **Direct YANG schema interrogation against C9K-4** — one session with NETCONF `<get-schema>identifier>Cisco-IOS-XE-acl` or RESTCONF `?fields` GET against an existing ACL (using admin creds that have RESTCONF read; the cluster's `AI_AGENT_RW` user has NETCONF-only access on this device). Output: the canonical YANG element names and nesting for an extended-ACL deny-host rule. Half engineer-day.
2. **One-shot translator update** to `aclRuleToYANG` covering whatever wrapper container or enum-leaf naming the v35 attempt missed. Mechanical once #1 surfaces the schema. Half engineer-day.

Then re-run test 08; no further engine changes expected. The Wave-10 confirmed-commit + auto-revert path is end-to-end-validated indirectly via test 10 today (`ConfirmedCommitUsed` event fired on the same device) and via engine-side envtest with race detection. The combined live proof of "ACL-write + confirmed-commit + auto-revert in one transaction" awaits the schema close-out.

**ACL writer feature-completion delta shipped today** (relevant to any future ACL operator workflow on this branch, not just test 08):

- `internal/drivers/iosxe/configdriver/writers/access_list_extended.go`: `aclRuleToYANG` translator (33 lines), wired as `nestedBodyShape`. Covers permit/deny action, IP/TCP/UDP protocol, host/network/wildcard source + destination, port matchers (eq/gt/lt/neq), log + remark.
- `internal/drivers/iosxe/configdriver/writers/access_list_standard.go`: companion namespace prefix on path + same translator wired. Standard ACLs benefit even though they don't have a current live test.
- `internal/drivers/iosxe/configdriver/writers/access_list_extended_test.go`: `TestACLRuleToYANG` with six cases locks in the field-by-field mapping so it can't drift.
- `internal/drivers/iosxe/configdriver/writers/nested_keyed.go`: `BodyShape` field on `nestedListSpec` + alias on the wrapper writer. Available now for any future nested-keyed family that needs netascode → YANG transformation.
- `internal/drivers/iosxe/configdriver/transport/netconf.go`: `orderedMapKeys` deterministic + key-first XML emit. Affects every nested write across every family.

## Files in this attempt

- `test-08-attempt.md` (this file) — the attempt's narrative.
- `test-08-final-status.txt` — the final CR `.status.familyStatus` + final per-pod kubelet log excerpt at the v32 stop point.
