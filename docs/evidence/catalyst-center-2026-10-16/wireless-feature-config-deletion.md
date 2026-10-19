# Feature config deletion — 2026-10-16

Feature-only Apply may now opt into `deletionPolicy: Delete`. The manager enrolls
`config.cisco.vk/catalyst-center-feature-cleanup` before the worker may apply that
policy. On CR deletion, the isolated worker cleans its known owned CleanAir/FRA
definitions using the existing pruning executor, REST transport and durable task
journal. It does not rewrite the stored desired source or remove its finalizer.

## Manager/worker boundary

The adapter registry has an optional `ConfigurationLifecycle` policy. Its
eligibility/completion callbacks are pure metadata validators over copied API
objects. The generic manager never imports a concrete adapter, constructs its
factory or calls its product API. Existing registrations default to nil behavior;
ND is unchanged. Policy registration is validated and defensively copied.

Only the trusted manager gains config metadata `patch` permission. Worker roles
remain config-read/status-write, without spec, metadata, finalizer or Secret
mutation rights. The manager requires an uncached APIReader, validates fresh
config/controller identity and completion evidence, and uses an optimistic
metadata-only patch. Other finalizers survive. Deleting-config status updates
wake this lifecycle; ordinary status observations retain the existing filter.
The standard manager wiring already supplies the uncached reader.

Both manager and worker binaries and the updated manager chart role are needed.
An unenrolled Delete-policy config waits before any new native application. No
extra public flag or CRD field is introduced. The example is
`examples/configs/catalyst-center/wireless-feature-delete-on-config-removal.yaml`.

## Deleting-config execution

Existing accepted/Unknown feature tasks are observed under their original source
and operation generation before cleanup. An unsubmitted metadata preparation may
be retired without a product call, including when its source is gone. Taskless
uncertainty, failed/corrupt results, source changes or unsupported operation kinds
retain protection. This observation grants no Create/Update submission permit.

A real Kubernetes API can advance CR generation during deletion. An explicit,
deleted-feature observation context permits the original operation at that
boundary while preserving its recorded generation and checking source identity,
compiled effect, owner and controller bindings. A genuine subsequent source
revision stays fenced. Finalize operations additionally bind the exact full spec
and deletion identity, allowing metadata-generation transitions only when those
bindings remain unchanged. The normal non-deleting generation gate is unchanged.

After pending work settles, cleanup uses an internal empty desired projection,
not a modified or fabricated config. The retained operation resource is
`feature-finalize:v1:<kind>:<nameHash>:<nativeHash>:<deletionToken>:<specDigest>`.
Only these bound Deletes may be newly prepared/submitted while the CR is deleting.
Existing native-state, profile-reference, capacity, one-use dispatch, native NCNC
Delete/UUID/absence proof, atomic tombstone and receipt-required release guards
remain in force. No task is replayed after an uncertain acknowledgement.

When no task requires the original source, retained ownership receipts suffice;
a removed source ConfigMap does not prevent cleanup. Imports and unknown native
objects are not adopted. Known closed work is rechecked through the same read-only
pruning snapshot before completion. A journal owned by another config is never
cleared; once this config's own work is verified closed, unrelated uncertainty
need not hold its finalizer indefinitely.

The worker reports `ControllerCleanupComplete=True` with a proof bound to config
UID/generation/spec/deletion timestamp, controller UID/generation and the exact
owned evidence digest. The manager independently validates retained closure and
that proof before removing protection. Retain policy performs no remote deletion
and still waits for its own pending task. Ownership/history remain on the
NetworkController after CR removal; this does not grant a new config UID takeover.

## Verification

The public worker matrix runs with metadata Update/Patch/Delete explicitly denied
while status writes remain available. Cases include cleanup, Retain, references,
native drift, lost acknowledgements, missing tasks, missing/pruned tombstones,
removed sources, in-flight Create/Update, changed source and unsubmitted preparation.
Completion proof mutations to config UID/spec, controller generation, retained
history or condition are refused. The public manager/worker handshake removes the
CR only after cleanup and preserves six lifecycle records. It also preserves
another owner's uncertain journal while finalizing already-closed owned work.

Final safety checks pass ([safety log](wireless-feature-config-deletion-safety.log)):
CC worker/handshake 17.647s, manager 2.890s and registry 2.024s. Manager tests verify
metadata-only patches, foreign-finalizer preservation, pending/stale completion
refusal and the uncached-reader requirement. An additional final worker check
(`/tmp/cvk-feature-finalizer-identity-final.log`) refuses a malformed prepared owner
identity rather than silently retiring it. Registration tests verify opt-out
compatibility, defensive copying and invalid-policy refusal. Worker RBAC checks
retain the existing secretless/status-only contract.

Actual CRD/status/deletion metadata persistence passes ten cases in 36.167s
([API persistence log](wireless-feature-config-deletion-envtest.log)). These servers
are private and ephemeral; the native service is a controlled fixture. An earlier
run exposed the deletion-generation issue for accepted tasks; it was fixed by the
observation-only path, rather than rewriting operation identity or authorizing
another submission. The failing run is not counted as qualification.

Feature/shared-journal/discovery/IPAM race regression passes 104.661s
([regression log](wireless-feature-config-deletion-regression.log)). Manager,
registry, ND, direct-device lifecycle and software-upgrade checks pass
(`/tmp/cvk-feature-finalizer-foundations.log`). Production packages build
(`/tmp/cvk-feature-finalizer-build.log`). The final closed-work observer and
uncached-reader changes have separate safety/API checks above.

## Native manager/worker qualification

The [native run](wireless-feature-config-deletion-native.log) passes in 35.87s
(race package 37.990s). The public manager enrolls the finalizer; seven desired
source revisions create/update both definitions through a status-only worker.
CR deletion then triggers two bound cleanup Deletes. Both acknowledgements are
retained as Unknown and recovered without resubmission. After proof publication,
the public manager removes its finalizer and the config is NotFound. The desired
Spec digest remains unchanged through deletion.

| Definition | UUID | Original Create task | Config-cleanup Delete task |
| --- | --- | --- | --- |
| `CVK_MB_16060401` | `334767e8-f3c0-45f6-91d5-8476e8fd1269` | `01a1434f-7972-7b1d-bd2b-0f5c99fdf67f` | `01a1434f-f019-75d2-8955-cfbc2e9c82dc` |
| `CVK_MB_16060401_FRA` | `89669867-5f98-477c-beaa-2de9052fdb03` | `01a1434f-740d-7835-8977-aed956bfbac7` | `01a1434f-f5e1-7683-baba-27913d8dd9bb` |

Private evidence directory:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-4037562824/`.
`complete.json` records configDeleted=true, managerFinalized=true,
workerStatusOnly=true, cleaned=true, knownTaskRecoveries=16,
deletionTaskRecoveries=2 and retainedRecords=6. Each kind has exactly one POST,
six PUTs and one DELETE, with eight native mutation attempts. Both complete native
summary baselines are restored; no generated definition remains.

`deleting-config-complete.json` retains the bound completion condition before
manager removal. `deleting-controller-complete.json` retains original generation-3
creators, generation-9 Updates and both Verified cleanup Deletes, with no pending
operation. Metadata and native task evidence remain after config removal.

The native run uses indexed Kubernetes client fixtures and invokes the actual
public manager reconciler, not an installed manager Deployment. Worker metadata
methods are denied by the fixture wrapper; actual installed ServiceAccount
admission/authorization and rollout are not attested. API persistence and the
chart permission boundary are separately checked. The controller-runtime test
logger emits a setup warning/stack; the test ends PASS and native mutation counts
and baseline restoration are verified independently.

## Remaining scope

The NetworkController must remain active until dependent feature cleanup finishes.
Simultaneous parent deletion, worker handoff, cross-owner takeover, installed
rollout/scale and WLC/AP deployment remain unqualified. Unmapped/profile references,
external edits, missing task/source evidence and damaged ownership do not trigger
forced cleanup. Arbitrary lost post-release history is not reconstructed. Full
93-resource and whole feature-resource parity remain incomplete; no whole-resource
coverage flag is enabled by CR-deletion support.
