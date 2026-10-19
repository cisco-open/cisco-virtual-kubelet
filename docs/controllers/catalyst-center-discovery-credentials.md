# Credential-bearing discovery

CVK can execute a pure `inventory.discovery` Apply source containing projected
device credentials, using the existing controller worker, transport and
configuration journal. Creation, original-task recovery and no-op convergence are
qualified with controlled native responses and a real Kubernetes API server.
**Live credential delivery and installed-worker qualification remain pending.**
The lab has qualified refusal to adopt an existing job through a mutation-blocking
transport; that does not prove successful device authentication.

Use `examples/configs/catalyst-center/discovery-credentials-apply.yaml`
with a new unique discovery name and an exclusive inventory owner. Start with both
retention policies set to `Retain`; source pruning has a separate opt-in below. Inline and ConfigMap model sources are supported.
At least one job in this path must select a versioned credential leaf. Global-only
jobs may coexist in the same source and reuse the existing owned discovery
executor. A credential-bearing job may also reference existing global credentials
by name; every reference must resolve unambiguously to its current native ID.
See `examples/configs/catalyst-center/discovery-composition-apply.yaml`.

Current lifecycle support also includes manager-enrolled pure-credential config
deletion, described in [public credential discovery deletion](#public-credential-discovery-deletion).
The later preflight/preparation/execution notes record earlier implementation
stages; their pending enrollment statements are superseded by that section.

The [preflight refusal and replanning](#credential-discovery-preflight-refusal-and-replanning)
section records the current guard-certified recovery support. This supersedes
earlier pending statements for proven no-dispatch outcomes; taskless request
uncertainty remains blocked.

## Credential projection

Keep secret values out of the NetAsCode model. For each selected leaf, provide a
companion `<field>_version` marker and an inventory `secretRefs` destination such
as `/discovery/0/password_list`. Authorize its source alias on the
`NetworkController.intentSecretSources` list. The worker uses the existing
per-key projected Secret mechanism; it does not fetch a second credential store.
The version marker is CVK's delivery convention, not an upstream NetAsCode field.

| Model credential leaf | `value` in the common versioned Secret envelope |
| --- | --- |
| `password_list`, `enable_password_list` | JSON string array representing the complete unordered password set |
| `http_read_credential`, `http_write_credential` | Opaque string matching the provider API contract |
| `snmp_ro_community`, `snmp_rw_community` | Scalar community string |
| `snmp_auth_passphrase`, `snmp_priv_passphrase` | Scalar passphrase string |

Each projected key contains exactly `{"version":N,"value":"..."}`. For a password
set the outer string contains the serialized array, as shown in the example's
comments. Do not put real credential values in Git, model fields or evidence.
Public usernames, SNMP versions/modes/protocols and descriptions stay in the
model. Invalid combinations, raw model credentials, projection lag and
unauthorized destinations fail before native I/O.

Never change secret bytes under an already applied version. Counters are the
write-only application contract; CVK does not retain hashes of device passwords
or read them back from Catalyst Center. A changed selected public input or advanced
credential version now requests guarded replacement of the owned discovery job.
The predecessor must have a v2 public-field receipt; an unchanged complete legacy
v1 pair can first acquire that receipt through the existing status-only promotion.
A changed source cannot manufacture the predecessor's missing public-field proof.

## Execution and recovery

CVK validates every declared job before selecting a single submission. It refuses
existing native jobs without its paired creation and credential-version evidence,
even if their public metadata matches. It uses the shared preparation and one-use
submission permit, with repeated source, secret, global-reference, native-state,
ownership and receipt-capacity checks.

The original successful task must identify the created discovery UUID. A complete
job with matching selected public metadata then allows a single status update to
retain creator UUID, credential versions and `Verified`. Reconciliation releases
the shared slot while preserving the receipts. A restarted worker recognizes
unchanged owned jobs without another mutation. Adding an unrelated credential
alias preserves completed receipts; active operations retain the existing
controller generation fence.

An acknowledged original task can recover after restart or timeout through the
shared Unknown/Verifying path. A lost submission permit acknowledgement or an
unknown response without a task ID remains fenced. Matching a job name is never
a retry permit. Missing/pruned receipts, a replaced UUID, metadata drift or changed
credential intent cannot silently become successful convergence.

`InSync` describes the declared job's verified configuration and execution
completion. It does not prove CLI, SNMP or NETCONF login succeeded. Assess those
operational discovery outcomes separately.

## Remaining boundaries

Config deletion, removal of the final projected credential within a retained job,
retained recovery for Delete/prune and deleted/deleting configs, metadata-only
NoAuthNoPriv jobs, extra inline metadata on global-only rows, and wider inventory
or template composition remain pending. The existing global-reference discovery
path retains its existing receipt keys and lifecycle; composed source pruning
reuses that executor for its global-only jobs.

See [qualification evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-public-execution.md)
and the [resource ledger](catalyst-center-resource-parity.md). Full pinned-model
parity and merge readiness are not established by this bounded addition.


Global-only jobs in composed sources keep their existing creator receipts and
owned replacement behavior. A global-only replacement does not rewrite the
credential-bearing job's receipts. Removing projected fields does not convert a
credential-owned job into an imported global-only job; that requires a future
qualified removal/migration flow. See the [composition qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-composition.md).


New creator receipts also retain a versioned fingerprint of their declared public
fields. Existing complete v1 receipts can acquire it through an idle status-only
update without replaying delivery. Original task/version/time evidence remains
unchanged. If the action-receipt budget cannot hold the added fingerprint, the v1
read-only no-op remains compatible. These fingerprints prepare the ownership
checks used by guarded replacement. Native v2 creation/promotion and
installed-worker qualification remain pending. See
[public-field proof qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-public-effects.md).

## Owned replacement

Replacement retains the predecessor's creator and credential receipts. Before
Delete, CVK verifies its exact UUID and selected public-field fingerprint, current
source and projection, exclusive owner, and room for both the deletion and
successor receipts. Changed intent can stop/delete an active owned discovery;
unchanged active jobs continue to wait. Submission uses the existing one-use
journal permit and no-replay transport.

The original successful deletion task and repeated complete native absence must
be verified before a single status update records retirement and `Verified`.
Retirement binds the queued versions without claiming they were delivered.
Generic credential publication and generic verification cannot settle Delete.
Only then may the shared loop create the successor and publish its own paired
creator/credential receipt. Both native operations retain their separate tasks.

Successor identities include the exact predecessor retirement. Returning to an
earlier public input therefore preserves earlier application evidence. Removing
some credential leaves retains their historical version floors; reintroducing
those leaves requires higher counters. Same-counter Secret source changes and
version rollback are refused. Removal of every projected leaf or migration to
another ownership family remains unsupported.

Known deletion tasks can recover through the existing Unknown path. Lost permit
acknowledgements, taskless outcomes, changed source during an accepted operation,
missing receipts, native drift, a remaining predecessor, or a foreign same-name
job remain fenced. Source changes after retirement must retain the queued input
before the successor can be created. This is not automatic source-loss cleanup.

Controlled and real Kubernetes API qualification are recorded separately from
live credential delivery. Actual device login, installed replacement actors, and
native credential-bearing replacement remain unqualified. See
[replacement qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-replacement.md).

## Source pruning

Set `prunePolicy: Delete` while retaining `deletionPolicy: Retain` to clean up
owned discovery jobs removed from the resolved source. Update `secretRefs` to
match the remaining leaves. To remove all jobs, use `inventory.discovery: []`
and `secretRefs: []`; retained credential ownership keeps this empty source on
the typed cleanup path. Removing fields from a job that remains declared is not
a request to remove the job or change its ownership family.

Before any prune, CVK checks all removed creators for exact UUID/public-field
ownership and available receipt capacity. It uses the shared one-use Delete
permit, original-task recovery, repeated absence and atomic retirement. A terminal
prune has no queued successor and publishes no credential-delivery receipt.
Existing creator and credential evidence remains. Unknown native outcomes,
missing proof, drift, foreign identities and capacity exhaustion remain fenced.
Old legacy receipts lacking a public fingerprint require restoration of their
original source and idle promotion before they can be pruned.

Unchanged reconciles and restarts do not recreate pruned jobs. Reintroduction
requires a later bound config generation or ConfigMap revision. The successor
retains the terminal receipt in its contextual identity and cannot reset historic
credential floors. Global-reference jobs in composed sources keep the existing
ordinary pruning implementation and receipt keys. `Retain` performs no pruning.

The source-prune stage alone did not add retained Delete/prune recovery; the
follow-up below adds acknowledged-operation recovery. Config-deletion finalization,
cross-family migration, native credential delivery and installed cleanup
qualification remain open. See [source-prune qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-prune.md).

## Recovering acknowledged Creates after source loss

Public execution records validated, non-secret source identity before mutation and
retains canonical public fields in each new Create operation. An acknowledged
Create can finish its original task after its ConfigMap is deleted, changed or
replaced, or its projected file disappears. Its config, spec, controller and
inventory owner must remain unchanged. The original task, exact UUID and retained
public fields must verify before the common atomic writer records both receipts.
The shared slot is then released without another native request.

Recovery reports Waiting; it does not mark current intent InSync or apply a
changed source. A valid source/projection must return before ordinary planning
resumes. Original source/task/version identities remain in the audit. Normal
execution keeps the full projection contract; the source-loss branch validates
the full shared metadata contract plus the frozen bindings needed to finish
acknowledged work. Secret references remain present during validation: aliases,
uses, JSON pointers, descriptor capabilities, model versions and sections are
checked even when their projected files no longer exist. Ordinary dispatch still
requires the original mounted-file contract.

Scope loss/tampering, config/controller changes, revoked owners, failed/pending
tasks, native drift and missing receipts stay fenced. Older operations without
retained public scope still need their original source. Prepared, Submitting and
taskless work is never replayed. Recovery during config deletion or after the
config object disappears is not enabled. A follow-up extends recovery to
acknowledged Delete/prune operations as described below. See [source-recovery qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-source-recovery.md) and the earlier
[scope prerequisite](../evidence/catalyst-center-2026-10-18/discovery-credential-retained-scope.md).


## Recovering acknowledged Delete and prune operations

When the source ConfigMap or credential projection disappears after an owned
Delete is acknowledged, recovery uses its original task and exact creator pair.
It requires the pre-submission source attestation, unchanged config/controller
identity and ownership, complete retained history, and repeated native absence
of both the original UUID and any same-name replacement. Terminal pruning also
checks the originally attested model digest and explicit `prunePolicy: Delete`.

The normal and retained paths share one atomic retirement receipt writer. A
Delete never publishes credential delivery. Recovery stays Waiting, and loss of
the source cannot authorize another Delete or the queued successor Create. The
real source and valid projection must return before planning resumes. Taskless
outcomes, failed tasks, remaining original jobs, foreign jobs and missing authority
remain fenced. This does not enable config-deletion finalizers or native
credential mutation qualification.

See [retirement recovery qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-retained-retirement.md).

See [shared cleanup metadata contract](../evidence/catalyst-center-2026-10-18/discovery-credential-cleanup-metadata.md).


## Config deletion cleanup preflight

A private read-only cleanup preflight now binds the exact deleting config, current
source/projection, controller and retained creator/credential pairs. It selects
all owned creators through the same complete-cohort checks as source pruning,
then repeats native UUID/public-field and Kubernetes/source checks. Active owned
jobs are eligible for observation; foreign jobs, missing jobs, legacy public
proofs, unknown receipts and changed generations are refused.

This observation is not a dispatch permit. Credential-bearing Delete-policy
configs are still excluded from public enrollment and finalizer release. No
native Delete, ownership retirement or credential application is published by
this preflight. Source loss, mixed ordinary/credential histories, in-flight work,
deleting controller/config changes and remaining lifecycle execution require
further implementation. Full config-deletion support is not available yet.

See [credential cleanup observation evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-cleanup-observation.md).


## Deletion-bound cleanup preparation

A private preparation step now reruns cleanup preflight and reserves one exact
owned target through the existing configuration journal. It reuses the versioned
`discovery-finalize` deletion/spec/evidence envelope with a typed credential
prune identity inside. New executable credential records use v2, adding a source
UID/revision/model commitment; ordinary discovery keeps v1. Source UID/revision, target UUID and creator-pair proof
remain bound. Ordinary and credential cleanup share the final status CAS and
readback implementation. Preparation creates no credential-delivery receipt.

The prepared record is classified as credential retirement for capacity and
receipt enforcement. It is rejected by ordinary discovery execution and the
active-config source-loss recovery path. Generic verification and journal finish
cannot bypass its required retirement proof. A plain observation has no preparation
marker and cannot grant this admission. Repeating preparation while the journal
is occupied cannot replace the existing record or issue native requests.

The private execution stage below adds native submission and acknowledged-task
recovery. The completion stage below adds manager validation and release for an already-held
finalizer. Automatic enrollment/public orchestration remain pending; a complete
production Delete-policy credential lifecycle is not enabled yet.

See [credential cleanup preparation evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-cleanup-preparation.md).


## Private cleanup execution and recovery

The private executor rebuilds a prepared v2 record, validates current source and
projected credentials, and checks the full owned cohort before and after the
shared submission CAS. A final live permit/ownership/capacity check directly
precedes the existing no-replay exact-UUID Delete. Remaining phases never replay
that mutation: they poll the original task, verify repeated target/name absence,
and publish retirement plus Verified through the shared atomic writer. The slot
is released only after durable proof and another absence check. Owners, creator
history and delivered credential versions are retained; Delete claims no delivery.

Acknowledged completion can proceed after ConfigMap/projection loss or changes.
It checks the original deleting config/spec, controller/owner, baseline evidence,
creator pair, source commitment and task through the shared metadata contract.
It returns no fabricated source and cannot authorize another mutation. Initial
preparation/submission still requires the full real source and projection.
Changed config/authority, taskless uncertainty, failed/pending/foreign tasks,
missing receipts and native drift remain fenced. Post-permit refusals without
an acknowledged task retain Unknown rather than retrying.

The old v1 credential preparation format lacks a source commitment and cannot
enter native execution. Unsubmitted Prepared work needs safe retirement and a
fresh preparation; no automatic migration is attempted. Ordinary v1 discovery
remains supported. Automatic enrollment/public deletion orchestration, installed actors and native
credential cleanup qualification remain pending; the following completion stage
adds proof publication and release for already-held finalizers.

See [private cleanup execution evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-cleanup-execution.md).


## Worker completion and manager release

Once the journal is empty and every retained credential incarnation is retired,
the worker verifies native absence twice and publishes the existing cleanup and
ownership-retirement records. A credential-specific digest includes owners,
creator/retirement receipts and all delivered-version applications. Missing
history cannot become an empty successful cleanup. The worker uses status-only
permissions and shares the ordinary completion publication routine.

The existing manager independently validates the exact deleting config,
controller generation, owners, full journal digest and both canonical records.
It reads no source, projected file, Secret or product API. It can remove only an
already-held discovery finalizer; other finalizers and the credential audit remain.
Repeated/missing source data and projections do not prevent closure of proven
retired work. Ordinary ownership handoff deliberately rejects credential records
until that transfer path is qualified.

Automatic credential enrollment, public worker deletion orchestration, in-flight
pre-deletion drain, empty/unowned and mixed-history cases, credential ownership
handoff, installed actors and native cleanup qualification remain pending. This
completion stage does not advertise a fully supported public Delete lifecycle.

See [credential cleanup completion evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-cleanup-completion.md).


### Public credential discovery deletion

Pure inline-credential discovery can now select `deletionPolicy: Delete`. The
existing manager enrolls its discovery finalizer from authorized metadata; the
worker refuses mutation until enrollment and validates the complete model and
mounted credential projections before dispatch. Global-only mixed declarations
are refused before any credential Create under this policy.

Deletion observes original acknowledged Create or retirement tasks under their
original identity, source commitment and generation before starting owned cleanup.
Cleanup checks the complete owned native cohort before its first exact-UUID
Delete. It retains creator, retirement and delivered-version audit, and publishes
the existing completion records for the manager to release only its finalizer.
Known-task recovery does not resubmit mutations. Lost source/projection can be
tolerated while observing acknowledged work; further cleanup dispatch requires
restored, validated source and projection.

Cold first-Create Prepared, empty/unowned credential histories, taskless or
Submitting uncertainty, failed/foreign tasks and changed scope remain fenced.
Typed ownership handoff, mixed-history cleanup and installed/native credential
mutation qualification remain pending. This scope does not change the separate
ordinary discovery qualifications or establish full NetAsCode parity.

See [public credential lifecycle evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-public-deletion.md).


### Credential discovery cancelled before submission

A config deleted while its first credential Create is still `Prepared` can now
complete without submitting that Create. The worker validates the original
scope, complete source and mounted projection; it rebinds the exact first input
and checks all declared native names are absent twice. It then uses one status
update to clear the unsubmitted slot and retain the original operation in the
existing ownership-retirement condition. No creator, deletion or credential
application receipt is fabricated.

Recovery repeats these checks after a rejected or lost acknowledgement. The
existing completion writer and metadata-only manager callback validate the same
canonical witness. Ordinary ownership handoff refuses this credential record.
No new finalizer, CRD, transport or credential store is added.

An empty journal without the witness cannot establish success. Missing/changed
source, missing projections, native conflicts, delivery receipts, changed scope,
tasks or `Submitting` uncertainty retain the fence. General empty/unowned cleanup,
mixed Delete-policy sources, credential handoff, proven post-permit no-dispatch
recovery and installed/native credential execution remain pending.

See [unsubmitted cancellation qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-unsubmitted.md).


### Credential discovery preflight refusal and replanning

Credential Create, replacement Delete, source prune and source-committed config
cleanup now use the same process-local no-dispatch capability as existing CVK
configuration writers. Their complete cohort/source/projection/ownership/capacity
preflight sits outside the native request closure and inside the shared one-use
guard. Only that guard can certify a refusal before the request is entered.

The existing status CAS retires only the exact unsubmitted operation. Inventory
ownership and creator/retirement/credential-version audit remain unchanged.
A later public reconcile reloads current intent and plans a new operation ID.
No failed request is replayed. API errors, ambiguous or taskless responses,
foreign/spent proofs and changed operation/owner evidence keep their fence.
Legacy or source-uncommitted formats do not gain this authority.

Controlled and real API tests cover the proof boundary and public Create/config
cleanup replanning. Live qualification distinguishes actual read-only cancellation
from injected preflight failure; it does not establish native credential delivery,
installed worker rollout, or full NetAsCode parity.

See [proof and lab qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-no-dispatch.md).


### Credential handoff validation prerequisites

The manager's existing credential-completion callback now shares a pure canonical
retirement-record validator with a private handoff observer. The observer checks
paired closed history, old-config disappearance, the new enrolled pure-credential
source and frozen projection, delivered-version floors, stable resolved global
references and repeated native absence. It returns metadata only and changes no
owner, journal, source, finalizer or native object.

A new config UID must advance delivered counters for reused job/credential fields,
including previously removed leaves. Logical fields are compared independently
of discovery row indexes; queued versions on a retirement are not delivery.
A reappearing original UUID or a matching unowned native name blocks observation.

This qualifies prerequisites only. Public credential handoff remains disabled;
normal execution still rejects foreign credential lineages. Preserving and sealing
multi-owner audit through the ownership CAS and subsequent successor execution
must be implemented and qualified before enabling transfer. Installed actors and
native credential mutation/handoff remain open.

See [handoff observation evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-handoff-observation.md).


### Sealed credential history for later handoff

New verified credential closures add a fixed-size, versioned history seal to the
existing ownership-retirement record. It covers canonical copies of all typed
creator, deletion and delivered-version rows, while excluding mutable reservation
metadata. The shared manager verifier rejects altered or unknown seal versions.
Legacy single-owner closures remain readable; missing seals do not authorize
archived execution.

A derived cohort validator can now check several completely retired owners and
one active owner without modifying their original rows. It checks global
operation-ID collisions, native UUID reuse, complete pairs, mixed group-cancellation history,
closed archived lineages and active-owner credential floors. The same seal input
survives reservation metadata changes and row reordering. Removing an entire
owner changes the digest even if the remaining rows are structurally valid.

This is a prerequisite for execution after handoff. Public transfer and foreign
credential lineage execution remain disabled until the archive seal is bound to
controller/source identity, the ownership CAS and all later execution/recovery
checks. No new transport, journal, CRD or NetAsCode input field is introduced.

See [cohort and seal qualification](../evidence/catalyst-center-2026-10-18/discovery-credential-history-cohort.md).


### Private credential custody publication

A verified handoff observation can now drive a private atomic status update that
changes the inventory reservation and embeds its custody record in the existing
retirement condition. It repeats source/projection/native checks, requires the
known history seal and preserves the original secret snapshot across revalidation.
Controller identity/generation, exact initial successor identity/spec/source,
resolved-input digest and the predecessor record digest are retained together.
No receipt is rewritten and no native mutation or config/finalizer mutation occurs.

A separate metadata/readback validator recognizes a committed change after a lost
acknowledgement, validates the archived cohort/seal and reconstructs the immutable
predecessor view for the existing verifier. Replaying the old observation cannot
repeat the CAS. Legacy closures remain readable but cannot publish custody without
a seal. The private record currently binds the exact initial successor revision.

Public handoff and ordinary foreign-lineage execution remain disabled. Binding
this record into normal admission, current-intent updates, cancellation, recovery
and later retirement is still required before enabling the complete flow.

See [custody publication evidence](../evidence/catalyst-center-2026-10-18/discovery-credential-custody.md).
