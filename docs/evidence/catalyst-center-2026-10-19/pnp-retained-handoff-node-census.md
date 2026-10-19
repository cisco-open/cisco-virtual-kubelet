# Retained foreign handoffs and recognizable CVK Node identity

The shared unmanaged-identity census now observes another controller's sealed
metadata cohort before testing whether that controller has an active operation.
This closes the previous skip after certified refusal or journal completion.
The complete retained cohort continues to block overlapping source pruning or
physical bootstrap even when there is no active operation, no remaining Lease,
a release marker or controller deletion timestamp. Only explicit handoff
retirement removes that status-based claim from the census.

The current controller remains excluded by exact namespace/name/UID, allowing
its existing manager acquisition to reobserve its own original custody. Foreign
canonical disjoint cohorts remain allowed. Empty, duplicate, malformed,
noncanonical or unknown-protocol cohorts hold admission closed instead of being
interpreted as clearance. The census is read-only and never settles or retires
foreign custody on its own. The checks are shared by metadata cleanup and physical
bootstrap without creating another lock, transport or ownership ledger.

Metadata admission additionally rejects recognizable CVK Nodes with absent,
malformed or contradictory physical identity observations. It reuses the existing
Cisco provider label, managed-device annotation constants and Cisco OS signal,
plus the existing physical-identity consistency validator. Known unrelated CVK
Nodes remain allowed; matching observed aliases remain conflicts. Ordinary
compute Nodes and other virtual providers preserve their existing known-alias
collision behavior. The stronger unknown-Node prerequisite is metadata-specific;
physical bootstrap retains its existing observation behavior apart from the new
foreign-custody check.

## Controlled and actual API verification

Controlled tests cover retained, active, refused, released, deleting, late and
cross-provider foreign custody, same-name/different-namespace controllers,
unrelated cohorts and malformed/unknown protocols. A second global observation
finds a newly appearing handoff. Caller status and foreign retained data remain
unchanged. Both metadata and physical-bootstrap boundaries exercise these cases.

A separate matrix covers ordinary/other-provider Nodes, recognized provider/OS
and managed-owner markers, empty markers, known single/consistent observations,
contradictions, malformed observations, matching aliases and deleting Nodes.
A late unknown recognizable CVK Node also holds the second metadata census closed.

The actual isolated Kubernetes 1.35 scope test stages a foreign schema-valid
released/refused handoff with no active operation and no Lease. Its overlapping
serial remains held; a disjoint selected cohort is allowed. Actual object
resourceVersion remains unchanged by the read-only census. Schema-permitted
explicit retirement clears the conflict. Foreign actor provenance and original
fence acquisition in this fixture are staged; the test does not claim installed
manager authorization for creating that foreign record.

The same real-API test accepts an empty ordinary Node, refuses it once marked as
CVK without an observation, accepts unrelated consistent observations, refuses
contradictory observations and detects a matching serial alias. The existing
protected manager seal/acquisition and Pod-bound worker claim qualification then
continues successfully. Readiness, compiler observation and compatibility are
staged in those isolated actor tests. Separate public PnP API scenarios qualify
verified completion, certified pre-HTTP refusal and retained uncertain outcomes.

All final shared API/controller/aggregator/ND/maintenance/direct-upgrade/software
lifecycle/application tests pass, along with PnP/journal/SWIM regressions and the
main build. [Machine-readable results and source fingerprints](pnp-retained-handoff-node-census.json)
record the gates. Test/package event counts include parent tests and subtests.

## Catalyst Center lab regression

A fresh public-reconciler native cycle registered and exactly cleaned up:

- 6ad5c6b50bc0186771cb16bf / CVKC3B0F1D0
- 6ad5c6b80bc0186771cb16c0 / CVKC3B0F1D1

The transport allowed only the two generated registration payloads and exact
creator-UUID DELETEs. Both records completed preparation, manager-ready handoff,
original guarded submission, independent absence/tombstones, settlement,
retirement and public finish. Repeated completed reconciles did not replay.
Two final reads verified an empty PnP catalog and unchanged network inventory.
No switch claim/reset/upgrade or other native mutation was allowed.

The private completed journal is /private/tmp/cvk-pnp-cohort-113487672. It retains
two creators and two verified tombstones, with no operation or metadata handoff.
Its removed UUIDs must not be replayed. Native Kubernetes authority/storage was
simulated with JSON persistence; the real-API qualifications above are separate.
This is public-reconciler/native regression proof, not installed end-to-end proof.

## Remaining full-model work

Production manager/worker/enrollment/settlement compatibility and composition
remain disabled. A complete installed fleet/caller proof, cross-process active
operation drain and handling of artifacts with no recognizable identity markers
remain necessary. Generation/domain transitions, config/controller deletion,
missing-source finalization, physical PnP claim completion and wider pinned
93-resource execution/hardware gaps remain pending. Full-model completion and
merge readiness are not established by this qualification.
