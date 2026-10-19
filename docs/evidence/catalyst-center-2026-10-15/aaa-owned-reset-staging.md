# Owned AAA reset staging and native compatibility limits

**Native cleanup qualification did not pass. Public manifest reset/prune/delete
remains disabled.** Private owned inheritance planning/observation and local
regressions are implemented; no credential receipt is discarded on a successful
task without independent native state proof.

## Implementation

The private reset primitive requires Apply mode, explicit Delete prune policy,
an exact site-UUID/config-owner credential receipt with both valid version
bindings, and a mandatory caller guard for current source/ownership/receipt and
any required physical coordination. It sends no secret bytes and invents no
credential version. Original credential ownership remains intact.

Inheritance preflight walks the authenticated native parent graph to configured
AAA groups. Null ancestor groups continue the search; explicit empty groups stop
it. Missing/Global-only unconfigured inheritance is refused before PUT. The
planned observation binds target/path/parent IDs, local metadata and the observed
ancestor chain; changed identity or metadata before submission refuses the
one-use request. Failed, denied or uncertain attempts cannot replay that closure.

Verification reconstructs an acknowledged operation, checks complete task trees
before and after repeated guarded native observations, and requires the exact
null inheritance representation. Failed/running/future/changed task evidence,
source changes and changed readback cannot certify completion. Parent-valued
metadata without inheritance provenance stays pending. This observer does not
publish a cleanup receipt, retire the original credential receipt, clear an
operation journal or grant dispatch authority.

No new model fields, CRDs, admission protocols or other infrastructure provider
paths were introduced. Runtime compilation does not select these private reset
methods. Existing omission semantics remain unchanged.

## Pinned provider review

The pinned module 0.5.0 requests provider `~> 0.6.1`; this review examined provider
v0.6.1, not every allowed patch. [Source URLs and SHA-256s](aaa-provider-reset-source-review.json).
Its [payload mapper](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/model_catalystcenter_aaa_settings.go)
omits null Terraform attributes rather than defining omission as reset. Its
[delete implementation](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_aaa_settings.go)
submits empty objects for both groups, treats NCND-prefixed errors as warnings,
and removes Terraform state afterward. CVK must retain ownership when native
cleanup cannot be verified. Matching model scope does not justify losing that
ownership evidence.

## Native findings

1. [Mixed inheritance/unset](aaa-reset-unset-native-rejected.log) returned HTTP
   400, NCND00081/NCND00027: the empty client group lacked protocol and primary
   address. Empty-object unset is now gated before API access. It is never
   silently changed into inheritance.
2. [All-inherit without parent policy](aaa-reset-no-parent-native-rejected.log)
   returned HTTP 400/NCND00070. This prompted the configured-parent preflight.
3. [Identical parent/child positive policy](aaa-identical-child-native-unqualified.log)
   produced a successful native task but local child readback was null. Public
   reconciliation did not publish an explicit child credential receipt or reach
   InSync. Handling this controller optimization without inventing independent
   credential proof remains a separate gap.
4. [Distinct child policy and reset](aaa-owned-inherit-native-unqualified.log)
   successfully applied a second explicit profile to the child through public
   reconciliation. The source reused existing authorized aliases across four
   projected paths. Two site receipts were retained, the parent receipt remained
   unchanged and no parent replay was permitted. The private null reset was then
   accepted, but readback never supplied the required null/provenance evidence;
   the qualification failed and original receipts remained unchanged.

The final [query comparison](aaa-reset-readback-provenance.json) tested omitted,
`_inherited=false` and `_inherited=true`. All returned non-null group metadata
with no inheritance fields. Parent-equivalent values cannot distinguish copied
settings from inheritance. [Terminal task metadata](aaa-owned-inherit-terminal-tasks.json)
confirmed the accepted work had ended; successful tasks do not close this state
proof gap. No unsupported payload alternative was substituted.

The [documented GET contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/retrieve-aaa-settings-for-a-site/)
describes null as the inheritance representation when `_inherited=false`.
The [PUT contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/set-aaa-settings-for-a-site/)
also distinguishes null inheritance from empty unset. The lab evidence is
insufficient to advertise either cleanup lifecycle as supported.

## Local verification and cleanup

[Final affected race suite](aaa-reset-affected.log): **6.685 seconds**. Includes
AAA, policy-server, network-credential, SWIM/Swim and apply-example compilation.
New tests cover policy/receipt/guard refusal, unsupported unset, parent/ancestor
resolution, unset/missing parent, late identity/metadata/source denial, frozen
receipt input, missing acknowledgement, no replay, task failures/changes/future
completion, exact null/empty distinction and ambiguous parent-valued readback.

All test areas/children and registration UUIDs from these attempts were removed
with scope/metadata/reference checks, terminal-task checks and verified deletion:
[unset rejection cleanup](aaa-reset-unset-fixture-cleanup.log),
[missing-parent cleanup](aaa-reset-no-parent-fixture-cleanup.log),
[identical child cleanup](aaa-identical-child-fixture-cleanup.log),
[final parent/child cleanup](aaa-owned-inherit-fixture-cleanup.log).
The missing-parent cleanup reused the same script whose footer calls its rejected
request “unset”; the UUIDs and the corresponding native rejection above identify
the actual missing-parent attempt.

Cleanup removed only owned, device-free test hierarchies after accepted work was
terminal. No AAA request was replayed, no real Kubernetes journal was cleared,
and the empty authentication-registration baseline was restored. External login
remained disabled. The older planned-AP diagnostic scopes are outside this cleanup.

## Still required

- A version-qualified native contract or independent supported API evidence that
  proves local inheritance/unset, rather than merely parent-equivalent values.
- Correct handling of identical child policy/inheritance optimization.
- Public manifest/prune routing with explicit semantics, durable preparation and
  acknowledgement recovery, atomic cleanup receipts and ownership finalization.
- Installed-worker/admission and real authentication qualification.

The live harness uses real appliance requests and simulated Kubernetes
persistence. JSON acknowledgement round-trip is an observer reconstruction test,
not API-server admission or durable cleanup-journal qualification. Full pinned
NetAsCode parity remains incomplete.
