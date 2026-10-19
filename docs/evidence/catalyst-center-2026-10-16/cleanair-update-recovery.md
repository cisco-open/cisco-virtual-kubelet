# CleanAir acknowledged-update recovery — 2026-10-16

A compiled feature-template plan can now observe a retained CleanAir Update task
from Unknown through the existing journal's Verifying/Verified/release sequence.
The callback grants no submission permit, task inference or create ownership.
The common reconciler still requires unchanged config UID/name/generation,
controller policy, source identity and section ownership. Completion still needs
native task ID/time correlation and success, followed by desired readback of the
original target UUID. A same-name replacement cannot satisfy that verification.

The adapter-local selector binds only selected CleanAir names in the exact
namespace emitted by the compiler: root `cleanair:<name>` for a sole collection,
or `cleanair/cleanair:<name>` beside FRA. Guessed/doubled prefixes, unselected
names and FRA resources cannot gain this recovery policy. Create/Delete,
missing target/task/time, physical/group/dependency/cancellation identities,
credential/history fields and unsupported preparation metadata are refused.
FRA-only and unrelated plans retain their previous behavior. No generic Unknown
fallback, journal phase, CRD/public field, provider interface, transport or upgrade path
was added or changed.

## Fixture and real-API verification

The public CleanAir restart test injects serialized Unknown states after native
acceptance. All seven updates (enable plus six directed band transitions) recover
without another write. Create uncertainty, missing task, source revision change,
foreign owner, replacement UUID, failed/pending task, foreign task response and
stale native task time remain fenced despite converged native metadata. Dedicated
selector cases cover both compiler namespaces and each unsupported identity;
selectors leave the retained operation unchanged. This suite passes with race
detection in 4.810s (`/tmp/cvk-cleanair-recovery-scoped-tests.log`).

Private Kubernetes API qualification runs the same public flow with actual
Config/Controller CRDs, UIDs/generations/resource versions and status subresources.
Known-task recovery and missing-task/Create/source/owner fences all pass (8.00s).
The pre-existing band update sequence also passes (7.46s; combined race package
17.609s, `/tmp/cvk-cleanair-recovery-envtest.log`). Native HTTP here is a fixture;
the installed lab cluster is not used or repaired.

Wireless/composition and resource completion/recovery race regression passes
14.725s (`/tmp/cvk-cleanair-recovery-regression.log`). Shared configuration journal,
discovery and IPAM ownership/recovery checks pass 18.272s
(`/tmp/cvk-cleanair-recovery-journal-regression.log`). ND, IOS XE software lifecycle,
software-upgrade and migration checks pass, with migration 1.556s
(`/tmp/cvk-cleanair-recovery-foundations.log`). Complete model tests and
report/apply example compilation also pass (examples 2.447s;
`/tmp/cvk-cleanair-recovery-model.log`, `/tmp/cvk-cleanair-recovery-examples.log`).
Strict documentation and whitespace checks pass.

## Native public-reconciler qualification

An initial seven-revision native run recovered all six acknowledged band updates,
retained one UUID and cleaned the generated definition (12.38s, package 14.303s;
`/tmp/cvk-cleanair-recovery-native.log`). A second run added a transport guard that
permits only this generated definition and one PUT per intended revision. It
counts attempted mutations, preventing recovery from silently consuming the next
revision's allowance. It never logs headers, credentials or request bodies.

The counted run used `CVK_MB_16024333`, UUID
`0be98f0b-4150-4580-844c-3903262c4b0f`. Each accepted update was serialized and
retained as Unknown, then a fresh public reconciler observed its original task
and returned to InSync. Tasks in band-transition order 2.4 → 5 → 6 → 2.4 → 6 → 5 → 2.4 GHz:

- `01a14297-eccc-747f-844d-43f9318b21eb`
- `01a14297-f2c9-73ec-a92c-8e2b7e9243cb`
- `01a14297-f8e4-7281-8028-2765f9f1f8d8`
- `01a14297-feea-71c7-a18e-6ea9f62128e7`
- `01a14298-04b5-7742-8f7c-ae977e52c5d4`
- `01a14298-0aaf-7acf-aa6d-d5c8f0b20c00`

Exactly one POST, six PUTs and one qualification-only exact-ID DELETE were
attempted. No duplicate mutation was dispatched during recovery or no-op.
Cleanup task `01a14298-0f82-7e0b-9b1e-3944b52cfcf5` completed and the complete
summary baseline was restored. The test passes in 11.80s (race package 13.707s;
`/tmp/cvk-cleanair-recovery-native-counted.log`).

Private evidence directory:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-1914757219/`.
It contains six serialized Unknown controller snapshots, seven converged native
snapshots, final controller/config state, cleanup receipt and `complete.json`
with cleaned=true, knownTaskRecoveries=6, posts=1, puts=6, deletes=1 and
mutationAttempts=8. Native qualification uses a Kubernetes client fixture;
real-API persistence is separately verified above, not an installed end-to-end actor.

## Remaining limits

This closes acknowledged Update task observation for the supported definitions.
It does not qualify taskless or uncertain Create recovery, durable result ownership,
source/CR-deletion pruning, adoption, cross-config handoff, FRA task recovery or
an unattended combined FRA/CleanAir workflow. Grouped CleanAir namespace selection
is fixture-tested; the native recovery run uses CleanAir alone. Installed worker
restart, all interferer/band combinations, WLC/AP deployment and RF/client behavior
remain unqualified. Full resource and whole-model parity remain incomplete.

Subsequent FRA and combined acknowledged Update task observation is recorded in
[combined recovery evidence](wireless-feature-update-recovery.md); installed
actors and uncertain Create/taskless outcomes remain unqualified.
