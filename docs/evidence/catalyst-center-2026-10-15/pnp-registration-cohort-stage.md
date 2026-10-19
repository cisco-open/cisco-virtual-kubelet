# PnP multi-device registration stage

Subsequent [public routing qualification](pnp-public-registration.md) now exposes
this registration engine for PnP-only Apply while retaining the claim gate.

The private registration stage now executes the complete PnP registration cohort
from a resolved PnP-only NetAsCode inventory fragment. This is the database-registration
stage only. Public PnP Apply, physical site claiming, workflow completion and
post-claim preview sequencing remain gated.

## Execution, ownership and recovery

Every declaration is parsed before any write. Whole-cohort preflight checks
complete PnP and network inventories, retained registration ownership and the
current source/controller. One inventory pair serves the entire cohort rather
than issuing native requests inside the declaration loop. Pending registrations
must be absent for every declared physical member, including native inventory
stacks. Existing unowned records are never adopted.

The selected first pending record uses the existing one-use native registration
plan and shared write-ahead journal. Two complete cohort observations are repeated
before planning, and two more within the source/owner/active-operation bracket
immediately before native submission. A late conflicting second declaration
therefore prevents the first POST. The final selected-record absence check is
also retained. At most one POST or journal step occurs per invocation.

HTTP 200 plus the created native identity advances to Verifying. A restarted
worker independently reads back the exact acknowledged UUID, publishes ownership
through the existing atomic action-receipt writer, then retires Verified through
the shared completion gate. Unsubmitted Prepared work is retired before
replanning; malformed Prepared target/receipt metadata is refused. Lost, async or
ambiguous acknowledgements stay fenced without serial-based adoption or replay.
Missing UUIDs, foreign parent identity and missing finalized receipts cannot
complete. Later manifest claim revisions reuse retained registration ownership
without silently recreating a missing/replaced native record.

All desired claim fields remain in the guarded source, including future image
and template intent. Registration does not attest that those claim dependencies
have converged. Stack registration sends only the first serial and stack flag,
as the pinned module does; the later claim stage still requires complete member
identity/admission. A true registration-stage result is never published as whole
onboarding InSync. No CRD, reservation protocol, transport or ND path was added.

## Fixture qualification

Inline and ConfigMap two-device cycles cover one-write sequencing, serialized
journal recovery, atomic UUID ownership, repeated no-op, claim-only revision
reuse, top-serial stack registration, later invalid/unowned/managed declarations,
late whole-cohort conflicts, source changes, lost/async responses, pruned UUIDs,
foreign operations/parent identity and missing receipts. A 1,000-device observation
with empty native inventories asserts exactly one PnP GET and one network GET,
with no writes. Persistence and
worker identities are simulated, not installed-cluster qualification.

All PnP tests passed in 9.055 seconds before the final malformed-scope additions;
the final guards suite passed in 8.827 seconds, and final cohort/stack tests in
4.075 seconds. Broader LAN/PnP/inventory caller regression passed in 249.266
seconds. ND/model regression passed from cache. Adjacent logs retain these results.

## Native qualification and retained fixture

The lab accepted one generated nonexistent-serial registration:
`CVKEF6A2870`, native ID `6ad1123f0bc0186771caae30`. The stage reached
owned registration with worker reconstruction, file-persisted journal evidence
and repeated no-op; no claim/reset/device operation was allowed by the test.
Kubernetes persistence was simulated. This first run did not reload the JSON
journal between every step; the revised harness does so for subsequent runs.

The initial cleanup guard compared uppercase source serials with canonical
lowercase inventory and refused cleanup. Independent readback confirmed the
exact ID/hostname/PID, Unclaimed / Not Contacted state, no site and no main,
system or reset workflow. This was a harness comparison bug; the production
registration ownership checks had already used canonical serials.

Cleanup attempts then stopped before DELETE because the lab connection returned
an authentication error and transport failures. The SSH listener remained local,
but a new jump-host connection timed out. After the user restored connectivity,
read-only probing returned token/PnP/network HTTP 200 with one PnP record and
three network devices. Exact retained-UUID cleanup was resumed only after these
reads. Retained cleanup passed in 1.51 seconds (package 3.615 seconds),
with exact-UUID deletion, twice-observed empty PnP inventory and unchanged shared
network inventory. No registration was replayed.

A fresh corrected cycle then passed in 4.99 seconds (package 7.099 seconds).
Generated serial `CVKD8A0DE90` received native ID `6ad114c20bc0186771caae64`.
It reloaded JSON journal state and rebuilt the worker between transitions,
verified owned registration and repeated no-op, then removed only the exact
Unclaimed / Not Contacted UUID after two scoped observations. PnP inventory
returned to empty and the shared network inventory matched its saved baseline.
Two further stage invocations refused to recreate the removed owned record and
left its operation/receipt evidence unchanged. See
`pnp-registration-stage-qualified-native.log`. No claim, preview, reset or
physical device operation was submitted. This is registration-stage native
qualification with simulated Kubernetes persistence, not installed onboarding.

Private intent/journal/native response evidence is held outside the repository
under `/private/tmp/cvk-pnp-registration-1428771456/` and
`/private/tmp/cvk-pnp-registration-3206831953/`. No credentials or raw
native responses are committed. Fixture removal uses Cisco's
[exact-ID PnP database delete API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/delete-device-by-id-from-pnp/)
and is separate from public prune/delete support.

## Remaining parity

Public registration/claim orchestration, mixed PnP/ASSIGN/PROVISION sequencing,
real PnP identity admission, stack/group
coordination, platform/readiness/credential gates, correlated workflow/template
completion, post-claim preview sequencing, installed actors and physical lab
onboarding remain pending. Database registration success alone does not establish
full `catalystcenter_pnp_device` or NetAsCode parity.
