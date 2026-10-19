# Fabric authentication: shared group lifecycle

This extends the earlier [override model and private executor](fabric-authentication-overrides.md)
to the existing public `controller-configuration-group-v1` runtime. Fabric-only
Apply can update resolved Closed Authentication, Open Authentication and Low
Impact overrides on existing fabrics when the declared foundation has converged
and every affected member has a current managed CiscoDevice/Node identity.
It remains unqualified against an accepted native fabric update and installed
Kubernetes actors.

## Scope and durable identity

The complete source is compiled before preparation. Foundation drift blocks
the update; other authentication selections are validated before retaining the
first operation. Preparation binds all root fabric members and all descendant
zone members owned by that nearest fabric. It conservatively includes zones
regardless of their selected authentication mode; nested independent fabrics
are separate scopes. Native membership inventory uses the existing paginated
fabric-device reader. Cisco documents its fabric-ID scope and supported roles
in the [2.3.7.9 API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-fabric-devices/).

The collector resolves native UUIDs and canonical serials with the existing
inventory binder, then brackets shared managed-target resolution with repeated
native observations. Missing members, aliases/duplicate serials, replaced Nodes,
late membership/profile/zone changes and incomplete evidence return no partial
target set. No new device or Node identity rules were introduced.

The versioned `fabric-auth:v2:` resource binds five digests: logical site/profile,
hierarchy/fabric/profile native UUIDs, native before-values, proposed result and
physical footprint. Contract ordering is normalized. Descriptions are excluded
from hashes and preserved at dispatch; native Voice VLAN and omitted BPDU values
remain bound in before/result evidence. The existing target and parent slots
retain profile and fabric UUIDs; existing PhysicalGroup stores managed targets.
No CRD or protocol schema extension was added.

Prepared reconstruction must reproduce the original before/result/footprint
binding. A restart cannot silently accept an intervening native edit as its new
baseline. Legacy v1, malformed and unknown-version family members remain fenced.
Only canonical v2 Update operations pass group action validation.

## Existing runtime, one submission

Public reconciliation performs section/source ownership checks and uses the
existing preparation CAS. The observation-only plan cannot submit or create
reservations. The existing manager/worker runtime must establish every member's
reservation and live authority before the shared group permit is available.

Dispatch revalidates source, owner, journal, baseline and complete target set.
The existing submission permit moves Prepared to Submitting. Repeated native
observations finish before the final group dispatch permit is consumed once,
immediately before the single API request. The core authentication executor
still owns payload construction and receipt decoding. A known task is retained
even when its durable receipt fails; an uncertain response is not retried and
does not release devices.

The first dispatch fixture exposed an incorrect attempt to consume the final
permit in each repeated observation. The fix places its one-use check at the
core's final pre-submit hook. Accepted, denied, missing permit, changed baseline
or member, lost response and revocation after the request are now covered.

## Read-only recovery and atomic completion evidence

Accepted or task-bearing Unknown operations recover through the existing group
verification contract. Full task-tree success is checked independently of the
PUT response. Two complete native result/footprint observations must match the
recorded scope/result digests and exact physical targets; managed bindings are
also rechecked. A second task-tree read closes the result observation window.

Verified operation and the scope-bound ActionApplication are persisted together
through the existing atomic group receipt helper. Publication does not release
claims. Replacing a prior scope receipt uses the existing predecessor-retirement
checks. Completion, partial release and manager settlement continue through the
shared terminal lifecycle before the journal can clear and InSync can publish.

## Fixture evidence

The initial complete focused race suite passed in **14.298 seconds**. The final
suite, including report/apply examples, description-preservation revalidation
and the independent nested-fabric boundary, passed in **14.656 seconds**. The
broader fabric, authentication, configuration/admission/journal and SWIM race
suite passed in **210.987 seconds**. Nexus Dashboard tests passed using valid
cached results. Strict documentation and whitespace checks passed; the resource
ledger still matches all 93 pinned resource types exactly. Public cycles for all three
profile types use the existing simulated actor/HTTP fixtures and demonstrate:

- Explicit group installation, public preparation and all-member claims.
- One accepted PUT, restarted Accepted-to-Verifying-to-Verified recovery and
  atomic receipt publication.
- Shared terminal completion/release/settlement checks, including lost completion
  acknowledgement and partial release.
- Current-generation InSync and two fresh reconciler no-ops, with one native
  request across the complete cycle.

Negative cases retain work for a running task, failed child, foreign task ID,
changed Voice VLAN or membership, source/owner change, missing member, denied
recovery, incomplete publication members and denied publication. Lost publication
acknowledgement retains the atomically written Verified journal and receipt.
Preparation also covers missing/late native or managed identities, baseline
replacement, lost journal acknowledgement and group-field pruning. These are
fixture results, not native device or installed API-server qualification.

## Native limits and remaining scope

The current lab has three global templates and zero fabric sites. Native
read-only qualification covers global round-trip/no-op, missing-fabric report
and refusal of unready execution. No fabric or device mutation is claimed here. Native read-only global round-trip
and zero-fabric readiness tests passed in **5.026 seconds** for the package; see
`fabric-authentication-public-native-read.log`.
An accepted isolated fabric update, actual BPDU/port behavior, installed actors,
broader inherited/default combinations, combined prerequisite changes and
ownership-aware reset/removal still require qualification or implementation.
The earlier unidentified global raw-response discrepancy also remains open.

Use `examples/configs/catalyst-center/fabric-authentication-report.yaml` first.
The matching Apply example requires existing fabrics, managed members and the
explicit shared group runtime. Full pinned NetAsCode parity remains incomplete.

A subsequent [empty native fixture investigation](fabric-empty-native-qualification.md)
identified a ConnectivityDomain application lookup failure despite an installed
SDA package and verified local wired collection. All created resources were
removed; accepted native authentication changes remain unqualified.
