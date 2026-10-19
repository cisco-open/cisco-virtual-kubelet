# FRA and combined feature-template Update recovery — 2026-10-16

FRA now shares CleanAir's adapter-local acknowledged Update recovery policy.
The feature compiler binds only declared root/group resource names. The original
native target UUID, task ID, nonzero submission time and ordinary metadata-only
operation identity are required. The existing journal verifies unchanged
config/controller/source/owner bindings, observes the original task, correlates
native success and verifies desired fields on the original UUID before releasing
the operation. Unknown never grants another submission permit.

Supported compiler namespaces are `rrm-fra:<name>` and `cleanair:<name>` for a
single collection, or `rrm_fra/rrm-fra:<name>` and `cleanair/cleanair:<name>` in a
combined collection. Unselected names, guessed/double prefixes and unrelated
executors remain refused. Missing task/target/time, Create/Delete, physical/group,
credential/history/cancellation/preparation fields cannot acquire this policy.
No shared journal transition, provider interface, public field, schema or
transport changes were introduced. Both resources keep their existing separate
native readers, guarded writes, payloads and readback verification.

## Fixture and Kubernetes persistence

Pure FRA and combined public reconciler fixtures recover serialized acknowledged
Update tasks without duplicate writes. They also retain fences for missing task,
uncertain Create, changed source, foreign owner, replaced native UUID,
failed/pending task, foreign task identity and stale task time, even when native
metadata matches the manifest. Exact namespace/identity tests cover both resources
in root and grouped plans. Scoped tests pass with race detection in 5.485s
(`/tmp/cvk-feature-update-recovery-scoped.log`).

Real isolated Kubernetes API tests run the public lifecycle for pure FRA and the
combined model, including known-task recovery and Create/taskless/source/owner
refusals. All ten cases pass in 11.72s (package 14.049s), using actual CRDs,
UIDs/generations/resource versions and status subresources; native HTTP is a
controlled fixture. See [API test log](wireless-feature-update-recovery-envtest.log).
No installed kubeconfig, lab cluster recovery or worker rollout was used.

Wireless, configuration journal, discovery and IPAM race regression passes
32.475s (`/tmp/cvk-feature-update-recovery-regression.log`). Complete model, ND,
IOS XE software lifecycle, software-upgrade and migration checks pass (migration
1.578s; `/tmp/cvk-feature-update-recovery-foundations.log`). Report/apply examples
pass 2.225s (`/tmp/cvk-feature-update-recovery-examples.log`). Strict documentation
and whitespace checks pass.

## Native combined manifest

The [recorded native run](wireless-feature-update-recovery-native.log) passed
seven public manifest revisions in 23.96s (race package 25.914s). It used two
never-associated generated definitions:

| Definition | Native UUID |
| --- | --- |
| `CVK_MB_16030456` (CleanAir) | `c04199b6-0109-4d11-8661-dfbc0aabc389` |
| `CVK_MB_16030456_FRA` (FRA) | `35f50f65-700a-410a-85d4-86c9cd95c501` |

The first revision creates both through the ordinary accepted-task flow.
Six subsequent revisions each change both definitions. CleanAir traverses all
six directed 2.4/5/6 GHz band transitions. FRA changes sensitivity, freeze/status,
interval and band pair, including 5/6 GHz without sensitivity. Each accepted
Update is serialized and retained as Unknown; a fresh public reconciler observes
its original task and verifies the same native UUID. Every revision reaches
InSync and unchanged reconciliation leaves the operation released.

Separate transport guards allow exactly one POST, six revision-bounded PUTs and
one exact generated UUID DELETE per kind. All twelve Unknown recoveries complete
without another mutation attempt. The total is two creates, twelve PUTs and two
qualification-only cleanup DELETEs. No source pruning or ownership is inferred
from this supervised exact-fixture teardown.

Cleanup tasks `01a142ab-d1c4-75e6-8442-a5f06a89f081` (CleanAir) and
`01a142ab-d317-750e-b3e4-1536e8feec49` (FRA) complete, restoring both complete
summary baselines. Twelve distinct serialized Unknown journal snapshots, fourteen
converged native snapshots, both baselines, cleanup receipts and `complete.json`
are retained privately under:
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-982794666/`.
The native run uses a Kubernetes client fixture; actual CRD persistence is
separately qualified above, not as an installed end-to-end actor.

## Remaining limits

This qualifies acknowledged Update observation in pure and combined compiler
namespaces and the actual combined native flow. It does not resolve uncertain
Create/taskless outcomes, infer native task identity from matching metadata,
provide durable resource ownership/adoption or source/CR-deletion pruning, qualify
cross-collection late-edit admission, or attest installed worker recovery.
The earlier combined installed-worker run needed supervised FRA receipt repair;
this new native protocol qualification does not replace that rollout evidence.
WLC/AP association, device firmware eligibility and RF/client behavior remain
unqualified. Full resource, feature-template and pinned-model parity remain open.

Subsequent cross-collection prepared-write snapshot checks are qualified in
[cohort preflight evidence](wireless-feature-cohort-preflight.md). This does not
add automatic retirement of taskless journals after preflight refusal.
