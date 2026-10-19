# Bulk fabric-device manifest grouping and public preparation

The Apply route now accepts the opt-in CVK annotation
`cisco.vk/catalyst-center-bulk-fabric-devices: "true"` for retained,
fabric-only manifests without unresolved secrets. It compiles the entire
supported inventory/border-device projection through the existing strict
parsers. Unknown fields are refused; other model components are not silently
dropped. Module execution options remain outside the NetAsCode data model.

## Pinned source grouping

The [immutable module's root/zone cohorts](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_fabric.tf)
are preserved: a root-site cohort exists when the source has an unzoned device
at that fabric site, and includes that site's zoned devices too. Zone cohorts
retain their original zone declarations. A zone-only source does not manufacture
a root cohort. All root cohorts precede zone cohorts. Each complete cohort is
sorted by declaration name and split into stable ten-device batches, including
already-matching memberships. Only the native request's selected mask excludes
matching memberships.

Root projections are accepted by the existing source-bound preparation helper
only when the complete source declares that root cohort. Recovery uses the
original incoming IDs and resolved native parent fabric to restore the root
projection rather than accidentally interpreting acknowledged root work as zone
work. Zone-only source cannot authorize a projected parent operation.

## Shared preparation

Every batch must pass the existing repeated native/managed target observer.
Incoming declarations cannot alias physical/inventory identities across batches;
the same declaration in parent and zone cohorts must bind the same complete
immutable target. All observations are repeated before selecting work and again
inside the existing source-bound preparation immediately before journal CAS.
Late zone, peer, identity or dependency failures leave no earlier journal.

At most one pending batch is recorded, using the existing ownership/group-journal
CAS and complete incoming/peer/parent footprint. Preparation creates no claims,
leases or native requests. Restarts preserve the original Prepared operation ID.
Already-matching parents are skipped before preparing zone work.

## Deliberate incomplete boundaries

This route does not enable public dispatch. A Prepared operation remains retained
while independent source-bound device proof and final settlement are absent.
Native no-op readback reports Waiting with
`GroupConvergenceVerificationUnavailable`; it does not advertise completed Apply.
Acknowledged recovery continues before option parsing, through the earlier public
recovery route. Invalid options or unsupported model fields refuse execution.

Zone preflight still requires its native parent memberships to exist. A mixed
source whose parents are not yet present stays pending without preparing an
earlier root batch. Deferred parent prerequisites, full model composition and
sequential receipt-based advancement must be implemented before claiming full
root-to-zone bootstrap parity. Complete API-server/installed actor admission,
large-cohort performance, mixed border-role execution, update/delete/adoption and
accepted live mutation remain unqualified. No full completion flags change.

## Evidence

The affected race suite passed in 11.629s. Tests cover a 23-member root cohort
(10/10/3) plus its 11-member zone cohort (10/1), source-order invariance, unchanged
input, zone-only grouping, parent projection/recovery and refusal to invent a
parent cohort. Ten public cases cover preparation, mixed matching-parent/pending
zone, no-op, missing peer/runtime, invalid boolean/scope/retention, unsupported
fields and late zone prerequisites. Three adapter restarts keep the same journal,
with zero native writes or reservations. See [affected tests](fabric-devices-groups-tests.log).
These are HTTPS/fake-Kubernetes fixtures, not deployed actor qualification.

The broad race regression also passed: Catalyst Center 276.307s and shared
configuration 31.117s. See [full regression](fabric-devices-groups-full-regression.log).
That run preceded the final cross-cohort guard refinement; the final affected
suite above covers that refinement and the added mixed parent/zone fixture.
Strict documentation build and `git diff --check` passed.

Authenticated read-only lab checks returned HTTP 200 and zero rows for fabric
sites, multicast and multicast virtual networks. Accepted execution explicitly
skipped (0.31s, package 1.130s). See
[sanitized access](fabric-devices-groups-live-access.log) and
[prerequisite evidence](fabric-devices-groups-live-prerequisites.log).
