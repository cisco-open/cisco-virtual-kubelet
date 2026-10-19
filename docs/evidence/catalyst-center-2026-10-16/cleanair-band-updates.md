# CleanAir same-UUID band updates — 2026-10-16

CleanAir band changes now use the existing definition PUT, with the same native
UUID and shared task journal. This follows the pinned NetAsCode 0.5.0 band mapping
and provider 0.6.1 schema/model: radio_band has no replacement modifier, and Update
serializes radioBand into PUT. The former CVK-only band-change refusal is removed.
No replacement DELETE/POST, controller, public field, CRD or upgrade protocol was
introduced. Existing complete selected-state preflight, protected-system checks,
omitted-field preservation and one-use prepared dispatch continue to apply.

## Native executor qualification

An initial generated-only native probe completed 2.4 → 5 → 6 → 2.4 GHz with
same-ID PUT/readback and baseline restoration
(`/tmp/cvk-cleanair-band-probe.log`, test 4.63s, package 6.769s). This checked native
behavior before enabling it in the executor.

The guarded executor subsequently completed all six directed transitions:
2.4 → 5 → 6 → 2.4 → 6 → 5 → 2.4 GHz. Each update retained UUID
`d6123da2-8f79-4c46-a22b-773bfdcf0ef5` for generated definition
`CVK_16022824`, waited for correlated task success, verified desired fields,
retained an omitted description, rejected prepared-write reuse and reached no-op.
Tasks, in order:

- `01a1428a-1143-7a1c-b98b-78c95c57ccee`
- `01a1428a-18b3-718f-81df-e6da3e34547d`
- `01a1428a-1beb-7071-8dc2-861b4036cb3c`
- `01a1428a-1f4d-7e70-99fb-ec5600c645a8`
- `01a1428a-228b-7bc8-aa96-75885174f855`
- `01a1428a-2631-7526-8d0f-a969e5d56f4e`

Exact generated UUID cleanup task `01a1428a-2856-7762-84c1-f61ebb9b9d8d`
completed, restoring the full summary baseline. The live test passed in 9.54s
(package 11.482s; `/tmp/cvk-cleanair-band-executor-native.log`). The test also
retains the earlier native stale-edit and omitted-description checks.

## Manifest and persistence checks

The existing manifest restart/update test now covers create, enable update and
all six band transitions, retaining one UUID and exactly eight writes. A new
isolated real Kubernetes API test runs that same public reconciler sequence with
actual Config/Controller CRDs, UIDs, generations, resource versions and status
subresources; the native HTTP service is a controlled fixture. It passes in
7.82s (package 9.961s; `/tmp/cvk-cleanair-bands-envtest.log`). It never loads an
installed kubeconfig or changes the stopped lab cluster.

Focused CleanAir/combined preflight tests pass in 3.368s
(`/tmp/cvk-cleanair-band-tests.log`). Wireless/composition race regression passes
in 12.150s (`/tmp/cvk-cleanair-band-wireless-regression.log`).

## Native public manifest qualification

`TestLiveWirelessCleanAirManifestBands` passed seven revisions through the public
config reconciler against the actual appliance: 2.4 → 5 → 6 → 2.4 → 6 → 5 → 2.4
GHz. Each revision reached InSync, retained native UUID
`0361da5d-52b8-41a5-b8ca-aedab63c45a6` for `CVK_MB_16023324`, verified desired
readback and preserved the unrelated summary baseline. Each unchanged reconcile
left the journal operation released. Exact qualification cleanup task
`01a1428e-c97f-791a-8d7d-75bcb52e5866` completed and restored the baseline.
The test passed in 12.29s (package 14.208s;
`/tmp/cvk-cleanair-band-manifest-native-final.log`). Private evidence is at
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-1243054033/`:
controller/config journals, seven converged native snapshots, cleanup task and
`complete.json`. This native test uses a Kubernetes client fixture; real-API
persistence was verified separately, not as an installed end-to-end actor.

The first native manifest run reached its initial InSync revision, then the
qualification harness reused a stale Config resource version after its no-op
reconcile. The harness now refreshes Config metadata before changing the next
revision. Its retained UUID `e5cf9598-a358-474a-8d05-a05af5696e09`
(`CVK_MB_16023040`) was matched to the saved name/UUID/full serialized native
readback, deleted by exact ID with task
`01a1428e-0614-746a-b508-fbdd0a45eb79`, and unrelated baseline preservation
verified before the fresh run. Cleanup initially refused a nil-versus-empty
collection difference introduced by JSON evidence round-trip; comparing the same
canonical serialized projection fixed that harness issue without weakening
identity or attribute binding. The cleanup passes (0.61s, package 2.734s;
`/tmp/cvk-cleanair-band-retained-cleanup-final.log`). Both initial harness failures
remain recorded and are not counted as successful qualification.

Model, migration (1.320s), ND, IOS XE software lifecycle and software-upgrade race
checks pass (`/tmp/cvk-cleanair-band-foundations.log`). Final wireless/composition
and report/apply example checks pass in 12.265s
(`/tmp/cvk-cleanair-band-final-tests.log`); complete model tests pass separately
(`/tmp/cvk-cleanair-band-model-final.log`). Strict documentation builds and
whitespace checks pass.

## Limits

These are global definition updates. They do not qualify WLC/AP association,
wireless deployment, RF behavior, all interferer/band combinations or client
traffic. Durable CleanAir result ownership, imported-definition adoption,
source/CR-deletion pruning, taskless uncertainty and installed-worker recovery
remain unqualified. This band-only change did not enable retained Unknown task observation;
subsequent scoped Update support is recorded in [recovery evidence](cleanair-update-recovery.md). No claim of full resource or section parity is made.
