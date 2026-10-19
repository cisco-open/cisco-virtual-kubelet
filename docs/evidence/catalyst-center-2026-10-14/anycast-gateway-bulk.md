# Bulk anycast gateway native stage

The private bulk anycast planner reuses the existing singular gateway parser,
request-local native inventory, pool/fabric/VN/parent/release dependencies,
semantic matching and resolved payload builder. VLAN allocation computation was
factored out of the existing conflict checker so both paths use the same rules.
No new transport, authentication or mutation coordination protocol was added.

The pinned NetAsCode 0.5.0 module groups the bulk resource by fabric for ordinary,
anchored and zone gateways. This stage requires one resolved native fabric and
preflights the complete supplied batch. Parent/anchor/zone resolution still uses
the existing native dependencies; unresolved parents are not invented to submit
children early. The internal inventory bound is 1000 declared members; this is
a CVK implementation bound, not a claim of an appliance API request-size limit.

## Selection, guard and readback

Source selectors are sorted by site and pool, and duplicate selectors or mixed
native fabric IDs are refused. Existing semantically matching members are
preserved and excluded from the request. Native VLAN conflicts and conflicts
between two still-pending declarations are checked before any submission.
Omitted attributes on matching existing members follow the singular comparison
semantics, without inventing defaults for a new gateway.

The resource identity retains the original selected-member mask and a digest of
all declared payloads, native prerequisites and pre-existing member UUIDs. It
does not shrink to the remaining missing subset after partial creation. The
selection mask is canonical, bounded and validated before readback. Actual new
member UUIDs are excluded from the original selection digest, while replacements
of pre-existing members invalidate it.

One atomic-use native closure performs fresh full observations before and after
an externally supplied authority guard, then submits one array using the existing
task transport. Caller payloads, including ordered secondary-pool maps, are
captured independently of subsequent in-memory mutations. Denial, dependency
drift, success and lost acceptance all consume the closure; none permits replay.
Readback checks the original full batch and every declared semantic attribute.
Partial creation remains incomplete; changed intent, prerequisites or original
member incarnation cannot be accepted as completion.

This is native planning/readback, not public ownership, journal, all-member
admission, task completion, rendered-device proof or settlement. The external
guard does not itself manufacture any of those authorities.

## Verification

Native HTTPS fixtures cover complete and partial creation, existing-member
selection/no-op, omitted existing attributes, lost acknowledgement, denied or
missing guard, duplicate declarations, invalid later members, cross-pending VLAN
ID/name conflicts, native L2 conflicts, pool drift before/during authority,
replacement, wrong readback, source mutation, malformed mask and immutable
captured payloads. The earlier affected anycast/gateway/L2 race suite passed in
65.009s before the final omitted-attribute preservation case.
The final affected anycast/gateway/L2 race suite passed in 65.630s; see
[affected regression](anycast-gateway-bulk-regression.log). All 22 final native
batch cases, including mixed fabric/zone refusal, passed in 2.533s; see
[batch cases](anycast-gateway-bulk-cases.log). Strict documentation build and
`git diff --check` passed.

## Lab prerequisites

The authenticated read-only lab check found zero native fabric sites. Accepted
bulk execution was therefore explicitly skipped and remains unqualified; no
fabric, VN, gateway or task was created to manufacture a successful test.
The inventory check took 0.37s (package 1.295s). See
[prerequisite result](anycast-gateway-bulk-live-prerequisites.log) and
[sanitized access](anycast-gateway-bulk-live-access.log).

## Remaining scope

Public module option/grouping/chunk routing, complete source/owner guards,
shared journal and retained accepted-task recovery, task/device convergence,
ownership/adoption/delete behavior, installed actors, full anchored/zone batch
composition and isolated accepted live mutation/cleanup remain pending. No full
resource or section capability is enabled by this native stage.
