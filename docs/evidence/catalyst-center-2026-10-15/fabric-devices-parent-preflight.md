# Missing-parent preflight for bulk root/zone preparation

This extends [public grouping/preparation](fabric-devices-groups.md). A mixed
source can now prepare its first pending root batch even when its declared zone
devices are still missing native parent memberships. The entire root/zone source
is preflighted before retaining that root journal; zones are not skipped.

## Source-derived proposal

Complete root-cohort observations construct a parent proposal from the same
canonical desired native memberships. All root chunks precede zones, so the zone
preflight sees the complete declared parent cohort. No proposal is invented for a
zone-only source, a different fabric or an undeclared target.

The existing dependency observer is reused with an explicit preflight-only
proposal. Zone placement, native zone/fabric identity, assignment, provisioning,
desired payload, actual memberships and role constraints must still validate.
Existing parent memberships must match the source proposal. Missing parents may
be represented only by their validated declared future membership; actual parent
records remain a separate unmodified baseline.

Prospective observations carry an explicit deferred-parent marker and a digest
of future parent expectations. The complete physical footprint includes incoming,
current peer/parent and proposed future parent devices, using the existing repeated
managed CiscoDevice/Node observer. Cross-cohort complete target consistency is
required for peers as well as incoming declarations. All observations are
repeated around the source-bound preparation and shared journal CAS.

## Execution boundary

Only an actual root observation can be selected for preparation while a later
zone is deferred. Normal zone planning, dispatch, acknowledged recovery and
verification call the observer without a proposal and continue to require native
parent membership. A deferred zone cannot be prepared by the public route.
Proposals create no task, lease, reservation, membership or completion record.

Actual full parent readback clears deferral and produces the normal zone
identity. Prospective identities differ from actual parent-backed identities;
they cannot be reused as membership verification evidence. The added JSON digest
field is omitted on the normal path, preserving existing non-prospective evidence
semantics and shared transport behavior.

This closes missing-parent **preflight/preparation**, not the complete public
root-to-zone execution cycle. Public dispatch, independent source-bound device
proof, final application receipt/completion/settlement, receipt-based chunk
advancement, full model composition, installed actors and accepted live mutation
remain pending. No resource or section completion flag changes.

## Verification

The affected race suite passed in 12.175s. Six prospective-parent cases cover
complete source-derived preflight, wrong fabric, undeclared parent, role mismatch,
invalid body and missing managed future-parent binding. Normal zone planning is
refused in each absent-parent case, even with an otherwise permissive native
guard. A separate transition fixture moves native parent readback through absent,
partial and complete states: partial parents do not enable a complete zone batch;
complete actual readback allows private native zone planning and membership
readback; the old prospective identity remains rejected. That fixture does not
provide real device convergence or group settlement evidence.

Public mixed missing-parent preparation retains the root operation across three
adapter restarts, with no native writes or device reservations. A missing native
zone still prevents any earlier root journal. Existing complete/no-op, mixed
matching-parent/zone, invalid option/scope/source, missing peer/runtime,
native/managed identity and acknowledged recovery tests remain covered. See
[affected tests](fabric-devices-parent-preflight-tests.log).

Full race suites passed on the final implementation: Catalyst Center 276.430s
and shared configuration 31.369s. See
[full regression](fabric-devices-parent-preflight-full-regression.log).
Strict documentation build and `git diff --check` passed.

The authenticated read-only lab check returned HTTP 200 with zero fabric-site,
multicast and multicast-VN rows. Accepted execution explicitly skipped (0.32s,
package 0.983s). See [sanitized access](fabric-devices-parent-preflight-live-access.log)
and [prerequisites](fabric-devices-parent-preflight-live-prerequisites.log).
No lab fabric, parent or zone was created to manufacture qualification.
