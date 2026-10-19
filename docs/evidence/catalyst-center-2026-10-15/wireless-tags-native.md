# Wireless profile site/policy tags: native qualification

Scope remains the pinned NetAsCode Catalyst Center 0.5.0 wireless profile, site
tag and policy tag resources. This work uses the existing network profile
compiler, configuration executor, native readers and task/readback verification.
No provider, transport, shared journal, ND or software upgrade contract changed.

## Native defects found and corrected

The [first run](wireless-tags-missing-ap-live.log) created a disabled OPEN SSID,
wireless profile and two empty areas, then assigned those areas to the profile.
The appliance rejected site-tag creation with HTTP 400 when apProfileName was
omitted. Both the documented
[create](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/create-multiple-site-tags-for-a-wireless-profile-in-bulk/)
and [update](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/update-a-specific-site-tag-for-a-wireless-profile/)
schemas require this field. The pinned NaC input makes ap_profile_name optional
and supplies no default. CVK now requires an explicit or retained observed AP
profile reference when planning a site tag, rather than inventing a default.
Public Apply reports MissingSiteTagAPProfile and fails before journaling or
submitting prerequisite writes. Known retained references still use current AP
inventory identity checks. Omitted-create semantics remain an upstream/native
model mismatch; they are not represented as supported default behavior.

Read-only inspection confirmed that the rejected request created no tags.
[Exact-identity cleanup](wireless-tags-retained-cleanup.log) removed the two
assignments, profile, disabled SSID and empty areas after verifying device
absence, no foreign child sites and no foreign SSID references.

A [second run](wireless-tags-null-placeholder-live.log) explicitly referenced
the observed existing default-ap-profile and received an accepted site-tag task.
Readback initially failed because the appliance adds an undocumented load:null
DTO placeholder. The reader now discards only that null placeholder. Non-null
load values and other unknown fields remain unqualified and block writes; no
load value enters the desired payload or an ownership identity.

The original accepted operation then passed
[retained readback](wireless-tags-retained-readback.log) with its original
resource binding, unchanged tag inventory and no replay. This used a temporary
fixture-specific read-only test. The reusable retained-model/receipt helper is
now part of the live test harness. The
[accepted-fixture cleanup](wireless-tags-accepted-cleanup.log) removed the exact
tag before the assignments, profile, disabled SSID and empty areas. No device
assignment, WLC/AP provisioning, reload or other device action was submitted.

## Regression and qualification boundaries

The [focused wireless profile race suite](wireless-tags-focused.log) passed in
3.859s, including preflight refusal before prerequisite writes, actionable public
status/no journal, null-only placeholder handling, non-null unknown refusal,
reference preservation, replacement races and existing manifest restart cycles.
The standalone retained receipt test is opt-in and read-only; its fixture must
still exist and its complete model must converge without another write.

## Policy-tag default zone and complete clean cycle

A [third run](wireless-tags-default-zone-live.log) accepted both tag creations
but exposed a native default: omitted/empty policy AP-zone selection returns
`apZones: [default-zone]`, even when the parent has no custom AP zones. The
reference/payload path now recognizes this built-in and maps empty selections
to it; named custom zones still bind to the actual parent profile. This does
not fabricate a custom AP-zone UUID. The original accepted policy operation
passed [retained readback](wireless-tags-policy-retained.log) in 5.773s, with no
replay, before [exact-fixture cleanup](wireless-tags-policy-cleanup.log).

The final [fresh native cycle](wireless-tags-native-final.log) passed in
**79.197s**, test body **77.28s**. It created a disabled OPEN SSID, wireless
profile and two absent random empty areas; assigned both areas to the profile;
created one site tag with an explicit existing AP profile and one policy tag
with built-in default-zone semantics; added the second area's membership to
both tags with the same tag IDs; and verified retained resource bindings,
fresh-compiler no-op convergence and drift-free reporting after each stage.
It removed exact tag IDs, assignments, profile, disabled SSID and empty areas,
then verified all captured public baselines restored. The prior uncertain
fixtures had been individually qualified and removed before this fresh run.
No physical device mutation occurred.

The [final profile race tests](wireless-tags-final-profile.log) passed in 3.671s.
[Manager/connector and worker RBAC checks](wireless-tags-runtime.log) passed in
3.789s. These are separate from the native test and do not prove cluster rollout.

 Installed
Kubernetes actor/schema recovery is not established by native compiler tests.
Custom Flex lookup, AP-zone combinations, ownership-based membership removal,
site-tag exclusivity/adoption across existing tags, full combined-model semantics
and actual WLC/AP behavior remain open. No full-resource parity claim follows
from this bounded qualification.

The final [wireless/network-profile race suite](wireless-tags-final-wireless.log)
passed in **7.857s**, after the built-in-zone adjustment. The broad affected
suite was launched before that final adapter-local adjustment; its result is
recorded separately and must not be described as a full final-revision run.

The [broad affected race suite](wireless-tags-full-regression.log) passed:
Catalyst Center **506.174s**; model, shared configuration, reservation provider,
ND, controller and managed protocol packages passed (cached). This run covers
the mandatory AP-reference/public-status and null-placeholder changes before
the final built-in-zone adjustment. Final zone semantics are covered by the
7.857s wireless/network-profile race suite and fresh native lifecycle above.
Strict documentation build passed in **4.21s**; whitespace checks passed.
