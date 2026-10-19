# Metadata cleanup: legacy enrollment and unknown consumers

This slice closes two concrete gaps on the path to installed PnP cleanup.
It does not enable the production metadata runtime or establish full-model parity.

The CiscoDevice manager now checks the configured shared serial-fence namespace
before enrollment and deletion in both managed and legacy topology. Previously,
a legacy device with no retained NodeIdentity skipped the check even when it
already declared the fenced serial. The barrier remains before finalizers,
teardown, projection, generated access and worker/config writes. An unset fence
namespace preserves the existing disabled installation behavior.

The shared device-enrollment helper still uses exact canonical serial keys for
known declared/retained identity. For address-only consumers it now makes two
complete, unfiltered reads of the configured fence namespace and refuses any
reserved bootstrap key. Labels, release markers, malformed content, holder and
TTL cannot turn retained custody into absence. Truncated/denied reads and late
fences hold the barrier closed. Source UID/generation/spec/retained identity and
resourceVersion are checked around inspection. The existing aggregator mutation
guard reuses this helper and rechecks before later transactions; finishing a
guard never erases retained custody. The chart's existing manager Lease-list
permission covers these reads; no new RBAC permission was added.

Complementing future enrollment protection, metadata manager admission now
requires every existing CiscoDevice in its complete global census to have a
valid declared or retained physical identity. Unknown/malformed consumers cannot
be assumed unrelated to a selected PnP serial. Known unrelated consumers remain
allowed, and either declared or retained matching aliases hold admission closed.
The existing physical-bootstrap prerequisite retains its prior known-alias
scope; the stronger census is selected only for metadata cleanup. No parallel
lease, transport, owner ledger, physical TargetSerial or native executor was added.

Operators must establish a real hardware serial before adding the write-once
CiscoDevice spec.physicalIdentity; these checks do not infer one from an IP or
manufacture status.nodeIdentity. Actual CRD enforcement still requires retained
node identity to match the declaration. Qualification initially used an invalid
retained-only synthetic state; the server rejected it, and the test was corrected
to valid write-once declaration and separate conflicting-device fixtures. The
schema was not weakened.

## Verification

Controlled reader tests cover disabled installation, legacy known/unknown
consumers, normal unrelated Lease traffic, foreign namespaces, malformed reserved
keys, retained release markers, late keys, denied/truncated namespace reads and
late device changes. Known disjoint serials do not require broad Lease listing.
Controller reconcile tests assert no enrollment/teardown writes under a hold;
aggregator tests exercise unknown identity and stale-absence rechecks.

A real isolated Kubernetes 1.35 API test exercises legacy known and address-only
devices, both before and during deletion. Reconcile returns a hold before any
write and leaves actual device resourceVersion/finalizers and fence UID intact.
Independent fence absence releases the read-only barrier. No manager workload,
device connection or production policy deployment runs in this test.

A separate real-API manager-scope test refuses a cross-namespace address-only
consumer, accepts its valid unrelated write-once declaration/retained identity,
refuses a separately declared/retained matching alias, then corroborates removal.
The existing protected manager seal/acquisition and Pod-bound worker-claim stages
continue afterward. Worker readiness, compiler observation and compatibility
callbacks in those isolated actor tests are staged. Public PnP API scenarios
separately pass verified cleanup, certified pre-HTTP refusal and uncertain native
response with retained custody.

Shared API/manager/aggregator/ND/maintenance/direct-upgrade/software-lifecycle and
application tests pass, as do PnP/journal/SWIM regression tests and the main build.
[Machine-readable gates and source fingerprints](pnp-metadata-consumer-enrollment.json)
record final results; test/package events include parent tests and subtests.

## Catalyst Center lab

A fresh public-reconciler native cycle created and exactly cleaned up:

- 6ad5c3b00bc0186771cb1678 / CVKC5FB7370
- 6ad5c3b60bc0186771cb1679 / CVKC5FB7371

The native transport allowed only the two generated registration payloads and
exact creator-UUID DELETEs. Both records completed preparation, manager-ready
handoff, original guarded submission, independent absence/tombstone, settlement,
retirement and public finish. Repeated completed reconciles did not replay.
Two final reads restored the empty PnP catalog and unchanged network inventory.
No switch claim/reset/upgrade or other device mutation was permitted.

The private completed journal at /private/tmp/cvk-pnp-cohort-3964836917 has no
active operation/handoff and retains two creators plus two verified tombstones.
Its deleted UUIDs must not be replayed. Kubernetes authority/storage in this
native run was simulated with JSON persistence. The real-API consumer and actor
qualifications above are independent; this is not installed end-to-end proof.

## Remaining scope

Production manager/worker/enrollment/settlement compatibility and composition
remain disabled. The complete retained-consumer census still needs review,
including other controllers' metadata handoffs when no active operation remains,
unknown orphan aliases and cross-process active-operation drain/rollout proof.
Generation/domain changes, config/controller deletion, missing-source finalization,
physical PnP claiming and the wider pinned 93-resource gaps remain pending.
These barriers do not drain an already-running external operation or prove
exclusive access to hardware outside the cluster.
