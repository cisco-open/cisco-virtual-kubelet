# Feature recreation and retired lineage — 2026-10-16

A newer desired-source input may recreate a CleanAir/FRA name after the same
config UID completed verified source pruning. This extends the existing feature
executor, REST transport, operation journal and atomic receipt publisher. It adds
no public spec field, CRD or shared provider contract. Ordinary creates, existing
imports and direct-device/ND/SWIM implementations keep their prior paths.

## Authority and atomic transition

Recreation uses the internal intent
`feature-recreate:v1:<kind>:<nameHash>:<effectHash>:<deleteOperationID>:<cohortDigest>`.
The parent is the exact preceding Verified Delete; the digest covers its complete
creator/applied/tombstone cohort, sorted by original resource identity. A later
config generation or changed source UID/version is required through the existing
`configurationInputAfter` predicate. Config UID/name and controller generation
bindings are checked with the existing durable source/owner permit.

Before planning and again after native preflight, the selected name and every
retired UUID for that logical name must be absent. A same-name native object is
not imported after a tombstone. Old UUIDs appearing under another name refuse
dispatch. The whole declared missing set must fit the existing receipt capacity;
archival does not free or silently discard history. A prepared POST is consumed
only once. Missing or changed tombstone/receipt/source/owner data cannot authorize
submission.

The original native NCNC Create task must return a canonical new UUID and match
the selected kind/name and declared fields. A returned retired UUID is refused.
Known acknowledged Unknown recreation tasks resume observation without replay;
lost acknowledgements and missing tasks remain fenced.

After proof, one status CAS archives the prior active cohort under
`feature-retired:v1:<original-result-resource>` and publishes the new creator plus
Verified. Original operation/task/source/native-state metadata and verification
times remain unchanged. The archive prefix reversibly retains the original result
resource. New applied records replace only **active** state. Subsequent pruning
adds a new active tombstone and never replaces previous history.

Lineage validation reconstructs every retired lifecycle and walks predecessors
from current creators. Each link requires the exact previous operation and cohort
digest, matching logical identity/owner, a newer input and ordered timestamps.
All archive groups must be reachable; missing members, duplicates, altered data,
UUID reuse, detached groups or cycles are invalid. Source generations alone are
not treated as native proof. Mutation admission and journal release require valid
lineage. If the API retains the new creator but prunes historical rows, publication
reports failure and the Verified operation cannot release. Other resource families
sharing the action store remain subject to their own protocols, rather than new
feature-specific validation rules.

Retain-mode reintroduction restores declared names without pruning omitted
resources. Delete mode can subsequently prune the recreated definitions. The
example is `examples/configs/catalyst-center/wireless-feature-recreate-apply.yaml`;
use the original config name/namespace/UID and its original logical names.

## Automated verification

The public matrix executes initial create/update/prune, then two complete
recreate/update/prune cycles with restart simulation, acknowledged Unknown tasks,
different native UUIDs, immutable original history and no-op replay checks.
Negative cases cover occupied names, retired IDs under aliases, missing tombstones,
foreign ownership, full projected capacity, taskless/lost-ack outcomes, UUID reuse,
simulated archive pruning and source/owner changes during native proof publication.
Additional checks refuse stale prepared POSTs after late tombstone/source/owner
changes and old-ID aliases, and refuse consumed-permit replay. Pure authority
checks reject the same deleted input and wrong predecessor/digest, while accepting
a later generation or changed source version. Retain restoration and the pinned
model example are independently covered.

Final safety/example checks, including shared-store resource-family isolation,
pass 28.157s ([safety log](wireless-feature-recreate-safety.log); private original
`/tmp/cvk-feature-recreate-final-isolation.log`).
Feature/shared-journal/discovery/IPAM race regression passes 89.693s
([regression log](wireless-feature-recreate-regression.log)).
Actual Kubernetes CRD/status persistence passes ten cases, including repeated
cycles, Retain restoration, archive loss, taskless recovery, projected capacity and
publication races, in 66.122s ([API persistence log](wireless-feature-recreate-envtest.log)).
These API servers are private and ephemeral; their native services are controlled
fixtures. They do not access the installed stopped Kubernetes cluster.

The first repeated-cycle test exposed a production bug: active Update publication
also matched and removed a retired Update for that logical name. The replacement
filter now preserves retired rows. Two-cycle and history-corruption assertions
verify the fix; no lineage gate was weakened. This failing run is not counted as
qualification.

Pinned model/coverage tooling, Nexus Dashboard, direct-device software lifecycle,
shared upgrades and migration-tool race checks pass
(`/tmp/cvk-feature-recreate-foundations.log`); unchanged packages use cached results.

## Native qualification

The [successful native run](wireless-feature-recreate-native.log) completes fourteen
source revisions in 67.96s (race package 70.107s). Seven initial revisions create
and update both definitions; revision eight prunes them. Two subsequent cycles each
reintroduce the original names, update their new UUIDs and publicly prune them.
All twenty-eight accepted operations are serialized as Unknown and recovered
without another submission. Twenty-two stored/native fingerprint comparisons
pass. Each kind performs fourteen native mutation attempts in total.

| Lifecycle | CleanAir UUID | FRA UUID |
| --- | --- | --- |
| Original | `e19d9f5d-2e23-49e0-97f0-292bd0dd4f1d` | `a575cdc3-b2cf-4cde-9725-9b005f733588` |
| Recreation 1 | `4f7a8485-e081-428b-b462-af280a7333e0` | `f7297b34-0b27-43b0-92ec-a70c33227f3f` |
| Recreation 2 | `17f9c895-581e-4479-89b1-25494599c604` | `81e19e44-bc78-47c1-a59e-c5247e592f00` |

Names are `CVK_MB_16052918` and `CVK_MB_16052918_FRA`. Private artifacts are in
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-747404022/`.
`complete.json` records publicRecreate=true, publicPrune=true, cleaned=true,
manifestRevisions=14, knownTaskRecoveries=28, nativeStatesChecked=22,
retiredCohorts=4, retainedRecords=18 and tombstoneRecords=2.

`recreate-complete-state.json` has twelve retired rows for four previous logical
lifecycles, plus six current creator/applied/Delete rows. Original generation-2
creators and generation-9 Deletes remain archived unchanged; the first recreated
lifecycle uses generations 10/11/12, and the final lifecycle uses 13/14/15.
Both native summary baselines are restored after each public prune. Final Delete
tasks are `01a14330-93e5-74cf-8202-e3a4e071461d` (CleanAir) and
`01a14330-9ae1-72f3-8531-808e10a26157` (FRA). No generated definition remains.

An earlier native harness run stopped after successful initial pruning, before
any recreation POST, because no-op checks changed the config resource version.
The harness now refreshes between phases. Both generated names from that run
(`CVK_MB_16052637` and its FRA companion) were separately confirmed absent and
both complete native baselines matched before a fresh qualification fixture was
created. The failed harness run is not counted as successful recreation.

The native run uses a Kubernetes client fixture; real API persistence is separately
qualified above. Physical WLC/AP behavior, installed-worker recovery, profile
association creation/removal and concurrent cross-controller transactions are
not attested. Retain-mode restoration is fixture/API qualified; the native repeated
lifecycle keeps explicit Delete policy for its final cleanups.

## Remaining scope

CR-deletion cleanup, cross-owner/controller handoff, explicit legacy adoption,
installed-worker/scale qualification and physical WLC/AP behavior remain open.
History uses the existing 256 action-record limit; automatic garbage collection,
arbitrary lost-history reconstruction and implicit archive truncation are not
implemented. Missing tasks or uncertain no-dispatch outcomes require evidence-based
recovery. The full pinned 93-resource model and these feature resources remain
partial; no whole-resource parity flag is enabled.


The subsequent [config-deletion stage](wireless-feature-config-deletion.md) adds
manager-owned protection and status-only worker cleanup on CR removal. The
remaining-scope list above records the recreation-only stage.
