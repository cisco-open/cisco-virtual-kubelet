# LAN link ADD preparation and one-use dispatch

## Execution components

The private LAN link ADD path now consumes the complete pinned 0.5.0 link source,
observes every declared link prerequisite, prepares one exact physical group in
the existing controller journal and dispatches one frozen native update through
the shared non-reconstructible all-member permit. Source, owner, controller,
managed endpoints, ports, topology, discovery origin, pool identity, usage and
activity are revalidated before dispatch. Another link's missing prerequisite or
unsupported explicit delete cannot leave an earlier add prepared.

The `lan-link-add:` family is reserved before schema/identity validation so a
malformed or pruned group cannot fall through to logical or single-device
execution. The Catalyst Center journal and owned-observation gates accept this
specific qualified family under `lan_automation`; generic Report/Apply support
and unrelated section behavior are unchanged. There is no new transport,
credential store, RBAC grant, CRD/status field or provider framework.

A successful native acknowledgement reaches Accepted. Missing/invalid receipt
or authority revoked after native acceptance retains Submitting and both claims.
Restart cannot reconstruct or reuse the submission permit. Dispatch cannot
complete, release or settle either endpoint reservation.

## Native preflight

The [dedicated active-session API](https://developer.cisco.com/docs/catalyst-center/2-3-7-4/lan-automation-active-sessions/)
supplies maximum count, active count and active IDs. Native string counts must
be exact nonnegative integers, agree with the unique ID list and fit the maximum.
Unknown/partial records cannot become idle. This initial preflight requires an
idle LAN service; target-disjoint concurrent-session semantics remain pending.

Pool usage comes from the modern IPAM detail endpoint already used by the
integration, using the [documented address-space counters](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/retrieves-ip-address-subpools/).
Usage is kept outside common IPAM desired-state structs and their dependency
hashes. The observed reservation/name/site/global-parent/CIDR must match the
bound LAN pool. Missing counters remain unavailable; malformed, overflowing,
inconsistent or beyond-CIDR counts are rejected. Unassignable, assigned and
default-assigned counts are subtracted conservatively. Fewer than two remaining
addresses holds an ADD preflight. This is not a contiguous-range reservation or
native allocator admission guarantee.

Activity and usage bind the repeated native/managed footprint. A pool-free
explicit delete invents no allocation dependency, but delete execution remains
unsupported until its retained ownership and recovery lifecycle is implemented.
The add-only executor refuses a mixed source instead of dropping its deletes.

## Fixture validation

- Native activity/usage and managed preflight race/refusal tests pass; see
  `lan-link-readiness-focused.log`.
- Source-bound preparation and shared one-use dispatch tests pass in 5.219s;
  see `lan-link-add-focused.log`.
- Additional full-source preparation tests pass in 3.177s; see
  `lan-link-add-source-focused.log`.

Cases include inactive/active/disabled service, inconsistent counts, missing usage,
exhausted pools, managed identity changes, unsupported sources, unresolved model,
missing runtime, explicit prune deletion, late unsupported delete or missing port,
missing/revoked permits, changed source, acknowledgement loss, post-acceptance
revocation, restart no-replay and pruned/malformed group identity. Native HTTPS,
Kubernetes authority and actor/Lease installation are fixtures, not installed
qualification. All dispatch tests retain both claimed endpoint reservations.

## Actual lab qualification

Authenticated native active-session read/re-read passed: maximum 5, active 0.
Modern usage readback was qualified on a uniquely owned temporary site hierarchy,
global pool and IPv4 LAN reservation. The /25 reservation reported 128 total,
0 unassignable, 0 assigned and 3 default-assigned addresses: a conservative
remaining count of 125. Reservation, parent pool and complete hierarchy removal
were verified by fresh native readback. No link update or automation action was
submitted to a switch. See `lan-link-readiness-live.log`.

An earlier read-only compatibility probe of the legacy reserve-ip-subpool API
returned HTTP 502. The implementation uses modern IPAM and does not derive
capacity from CIDR size or silently fall back to incomplete data.

## Remaining scope

Public execution activation, independent post-task convergence and final receipt,
worker/manager settlement, explicit delete ownership/recovery, add idempotency and
adoption policy, shared/cross-site pool allocation and active-session concurrency,
whole-model composition and installed actor/Lease qualification remain pending.
Current lab discovery history is empty, so accepted native link execution remains
unqualified. These private execution components do not establish full resource
parity or completion of the overall goal.


## Final regression

The complete affected race suite passed for Catalyst Center and its NetAsCode
parser, shared controller-configuration contracts and Nexus Dashboard. See
`lan-link-add-regression.log`. Additional full-source refusal cases are covered
by `lan-link-add-source-focused.log`. Strict documentation and whitespace checks
pass. Accepted device/link execution and installed actor/Lease qualification
remain outstanding; no full parity or overall-goal completion is claimed.


The subsequent [recovery implementation](lan-link-recovery.md) emits v2 identities
and adds public acknowledgement observation plus a private independent-proof and
atomic completion bridge. Legacy v1 completion remains fenced. Public new writes
and an installed proof provider remain unqualified.
