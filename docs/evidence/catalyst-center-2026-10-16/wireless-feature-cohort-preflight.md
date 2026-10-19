# Cross-collection feature-template preflight — 2026-10-16

A combined FRA/CleanAir prepared write now rechecks the original planning
observations of both selected collections. This includes a companion that was
already in sync or absent, its native UUID/system flag/typed details, its selected
siblings and desired in-memory input. A changed companion refuses dispatch before
the selected native mutation. Unselected valid definitions do not acquire this
fence. The native API still provides no atomic compare-and-swap between the final
reads and PUT, so this checks observable snapshots rather than a transaction lock.

Each child exposes its existing guarded write and original snapshot check from
one planning pass. The feature-only group reuses those checks and the existing
group resource prefixes, counts and verification. It checks companions first;
the selected child's one-use submit checks its own snapshot last. Already-converged
children still contribute a check. No extra planning/no-op inventory pass, new
transport, shared journal transition, provider contract, public field, CRD or
physical group/reservation protocol was added. Retained task recovery unwraps
only this known feature wrapper and retains the same qualified namespaces.

## Fixtures and persistence

Direct compiled-plan tests exercise FRA-first and CleanAir-first writes, including
changed companion fields, UUID, system flag, disappearance, malformed response,
desired input, concurrent appearance, ambiguous receipt and sixteen simultaneous
submission attempts. Valid unselected additions remain allowed. With one selected
definition in each collection, planning and no-op each issue four native reads
(summary/detail per kind); stable submission adds four recheck reads, with no
second planning pass. Scoped cohort/recovery tests pass with race detection in
5.842s (`/tmp/cvk-feature-group-fence-tests.log`).

Broader feature, configuration journal, discovery and IPAM regression passes
32.644s (`/tmp/cvk-feature-cohort-regression.log`). Existing pure CleanAir/FRA field
transitions, CleanAir retained-task/fencing cases and pure/combined feature
recovery all pass against actual CRD metadata/status persistence with controlled
native HTTP: package 35.535s. Individual real-API tests pass in 7.88s, 8.45s,
7.22s and 10.06s. See [API log](wireless-feature-cohort-envtest.log). These private
ephemeral servers do not use or repair the installed cluster.

## Native two-direction refusal and complete cycle

The [recorded native run](wireless-feature-cohort-native.log) passes in 32.68s
(race package 34.816s). Generated, unassociated definitions:

| Definition | Native UUID |
| --- | --- |
| `CVK_MB_16032108` (CleanAir) | `d31c92d0-08e0-44c6-a3a4-c8461ff1509e` |
| `CVK_MB_16032108_FRA` (FRA) | `d22013ce-42c9-4d98-a695-cfedcfd8976c` |

After the initial public manifest reaches InSync, two generated-only probes run:

1. Prepare a FRA update from the combined compiler. Change only its CleanAir
   companion's reporting flag using task `01a142ba-5cb0-7ffc-ab46-20e9f490c4aa`.
   The prepared FRA write refuses the stale cohort and its consumed permit cannot
   be reused. Mutation transport attempts remain unchanged. Restore CleanAir with
   task `01a142ba-5f04-7384-ade1-4a1e85b8f5bc`.
2. Prepare a CleanAir update. Change only the FRA companion's freeze flag using
   task `01a142ba-61ec-7b3a-aee9-44ac8e207bb5`. The prepared CleanAir write is
   refused without a mutation attempt and cannot be reused. Restore FRA with task
   `01a142ba-6442-7612-854b-c8feec4fe33c`.

Complete serialized typed readback matches both original definitions after the
probes. These private prepared-write probes run outside the config journal; they
do not attest automatic journal retirement after preflight rejection.

The ordinary seven-revision combined public manifest cycle then completes all
twelve serialized acknowledged Unknown Update recoveries and no-op checks. Each
kind has exactly one POST, eight PUTs (six manifest updates plus one probe edit
and one restore), and one exact generated UUID DELETE. Total native mutation
attempts are twenty; the two stale prepared writes contribute zero. Cleanup tasks
`01a142ba-c982-7071-8563-3f9d9d744f58` (CleanAir) and
`01a142ba-cada-7780-8591-06c5f994daf4` (FRA) complete and both full summary baselines
are restored.

Private journal, native snapshot, four probe-task receipts, probe completion and
cleanup evidence is retained under:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-1048289358/`.
The native cycle uses a Kubernetes client fixture; actual CRD persistence is
separately verified above, not as an installed end-to-end worker rollout.

Complete model, ND, IOS XE software lifecycle, software-upgrade and migration
checks pass (migration 1.542s; `/tmp/cvk-feature-cohort-foundations.log`). Report/apply
example compilation passes 2.242s (`/tmp/cvk-feature-cohort-examples.log`). Strict
documentation builds and whitespace checks pass.

## Remaining limits

A consumed stale preflight permit never authorizes replay. If the ordinary
journal already persisted Submitting before the callback refused dispatch, the
existing shared lifecycle can retain an Unknown operation without a task. This
change does not automatically retire that journal from matching metadata or
broaden taskless recovery. Explicit no-dispatch evidence/retirement remains open,
as do uncertain Create recovery, durable ownership/adoption, source/CR-deletion
pruning, installed actors and WLC/AP/firmware/RF behavior. Whole-model parity
remains incomplete.
