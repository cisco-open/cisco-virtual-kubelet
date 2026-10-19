# Wireless profile and tag public recovery

Wireless profile definitions, site tags, individual policy tags and all-new
policy-tag cohorts now reuse CVK's existing one-use submission guards, private
pre-dispatch refusal proofs and source/owner/journal-bracketed task observation.
This closes a metadata execution/recovery gap without another controller,
transport, operation store, CRD, credential channel or mutation-admission path.
The pinned NetAsCode 0.5.0 profile/tag model and native payload mappings are
unchanged. Existing Retain manifests gain the behavior without new annotations.

## Dispatch and acknowledged recovery

Profile Create/Update freezes its complete typed selected native observation,
used dependency binding and encoded body, plus an in-memory snapshot of the
compiled intent. Submission refuses changed native identity/content, dependency
IDs, intent or body. Nested source fields and nil/empty ownership distinctions
are retained in that intent snapshot. Site/policy tag and cohort writes retain
their existing full dependency/payload checks through the same one-use guard.
Aggregate prospective tag preflight can issue the same bounded proof before a
prerequisite write; the proof is not inferred from a REST error.

A guard-certified refusal retires only the exact current Submitting operation,
with unchanged controller, owner, source and journal evidence. The shared CAS
preserves retained applications and ownership, then a fresh reconciliation can
replan. Every planned write is consumed even on refusal or an uncertain outcome.
Missing acknowledgements, API mutation failures and old taskless Unknown state
cannot produce a refusal proof or authorize a retry.

A retained acknowledged Unknown operation is eligible only for recognized
profile/tag resource shapes in the actual compiled selection, with its original
task, parent/target and metadata-only journal envelope. Observation reuses the
original task root and complete task tree, stable independent native snapshots,
existing resource/dependency/payload verification, and common before/after
source, owner, controller and exact-journal checks. Neither a matching name nor
current desired values alone can settle a taskless outcome.

Normal Accepted task completion still advances to Verifying before independent
native readback. Partial cohort results therefore remain in Verifying; they do
not become a task-running condition or trigger another create. Accepted metadata
tasks now also require the complete task tree. Unknown recovery requires the
additional stable native convergence observations. Existing per-member cohort
verification, mixed-cohort refusal and membership-removal restrictions remain.

The implementation uses the existing resource identities and metadata journal.
It adds no creator/application receipt or per-object ownership. Create recovery
retains the ordinary unowned Retain convergence contract, rather than proving
which same-name native incarnation the controller originally created. UUID-bound
Update verification and dependency replacement checks retain their contracts.

## Operator use

Use the existing
`examples/configs/catalyst-center/wireless-profile-apply.yaml`,
`wireless-profile-tags-apply.yaml` and `wireless-policy-cohort-apply.yaml` manifests.
Their existing disabled-SSID, site, RF and AP-profile prerequisites still apply.
An acknowledged operation can recover automatically while its original source
and ownership remain valid. A taskless or inconsistent operation remains
retained for evidence-based investigation; deleting its journal is not a retry
mechanism. No new field opts into weaker checks or bypasses physical admission.

## Qualification

[Final focused race tests](wireless-profile-recovery-focused.log) include 32
public lifecycle cases and 16 journal-envelope refusal/selection cases, plus the
existing partial-readback phase regression. The 32 cases cover single and cohort
create/update with original-task interruptions, certified refusal/replan,
lost acknowledgement, failed child tasks, unavailable trees, pending roots,
managed native drift, source changes and owner replacement. The same 32 lifecycle
cases run against an [actual temporary Kubernetes API](wireless-profile-recovery-api.log).
Assignments and switching operations remain outside the new metadata selector.

[Broader race regression](wireless-profile-recovery-regression.log) covers
wireless definition/credential/feature/profile behavior, switching profiles,
shared no-dispatch proofs and template task recovery. [Shared foundation checks](wireless-profile-recovery-foundation.log)
cover controller/ND, configuration admission, device operations, diagnostic,
software lifecycle, software upgrade and direct GNOI packages.

The [native qualification log](wireless-profile-recovery-native.log) records
actual Catalyst Center tasks and readback. Each workflow creates a generated
disabled OPEN SSID, an unassigned profile and two empty areas. One local preflight
GET failure is injected per metadata action. Public reconciliation retires the
proven unsent operation, submits one native mutation, retains its acknowledged
operation as Unknown, and restarts observation of the original task and full tree.
Public status must reach InSync, with exact mutation/proof counts and repeated
no-op. Independent generated-fixture cleanup restores the original baselines.
Kubernetes storage, actor interruption and the preflight fault are fixtures;
the native API, task roots/trees, readback and cleanup are real.

The single-tag cycle passed in 274.02 seconds with six public metadata actions.
The custom policy-cohort cycle passed in 357.20 seconds with seven actions,
including one bulk cohort Create and later individual same-ID updates. Across
both workflows, thirteen proven refusals retired, thirteen original acknowledged
tasks recovered through full trees, both configs reached InSync with repeated
no-op, and both original baselines were restored by exact fixture cleanup.
The full native test command passed in 633.282 seconds. Existing
profile/site assignment and site/SSID setup/cleanup helpers supply scaffolding;
those operations do not gain the new recovery contract. Custom RF profiles are
existing read-only dependencies, not created or changed by these tests.

A [bounded root-task identity probe](wireless-profile-task-correlation.json)
queried one generated profile Create task. It found no structured `data` result
containing the native profile UUID. This is a single observed response, not proof
that every native service/result endpoint lacks such correlation. No creator
ownership or deletion permission is inferred from free-form progress text.

[Source fingerprints](wireless-profile-public-recovery-sources.json) identify the
reviewable implementation and pinned model mapping. They do not attest an
installed binary. [Strict documentation qualification](wireless-profile-recovery-docs.log)
checks the documentation and evidence links.

## Remaining full-goal gaps

Creator ownership, durable native baselines, per-object adoption/handoff,
membership removal, source prune/delete, mixed existing/new policy cohorts,
larger native cohorts, custom Flex lookup, broader VLAN/interface/zone behavior,
WLC/AP effects and installed actors remain open. Assignment-specific Unknown
recovery and other infrastructure/provider paths retain their own contracts.
No device assignment, provisioning, software upgrade or installed-cluster repair
was submitted. Full 93-resource pinned-model/schema/default/lifecycle parity and
branch merge readiness are not established by these metadata workflows.
