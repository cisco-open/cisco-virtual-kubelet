# Discovery cleanup: private completion attestation

A private read-only completion observer now reuses the cleanup evidence validator
and owned discovery absence checks. It supports closed owned cleanup and empty or
unowned state without claiming a section or adopting a native job by name. A
status-only publisher emits a canonical bound proof; a pure metadata predicate
validates that proof without constructing a product client or reading credentials.
The public registry, worker dispatch and manager finalizer enrollment are unchanged.

The observer requires an idle journal, no owned Create receipts, valid unique
owned Delete receipts, complete ownership/custody checks, two native catalogue
reads and unchanged config/source/controller/evidence snapshots. Foreign jobs and
foreign ownership are retained. Completion is not a native mutation permission.

The proof binds controller/config UIDs and generations, full config spec and
request deletion digests, complete retained evidence and the observed source
snapshot/model digest. Inline source digests are independently checked by the
pure predicate. ConfigMap UID/version/model are worker attestations; the pure
predicate does not reread the ConfigMap or the appliance. Changes to retained
evidence or bound config/controller identity invalidate the proof. New publication
still requires an available pure-discovery source. Workers write only config
status and leave both the reserved discovery finalizer and unrelated finalizers
in place. Public manager release is not enabled.

## Tests

Twenty controlled and real-API modes cover closed, nil/empty/owner-empty state,
unowned matching names, foreign owners, ConfigMap source, remaining creates,
native reappearance, invalid/duplicate/aliased receipts, active work, foreign
sections, unavailable/mixed source and evidence/native/spec/source races. Each
of seven successful modes also rejects eight config/controller/proof mutations.

The initial API run rejected duplicate-key fixture rows at CRD admission. The
corrected rows have distinct list keys while retaining logical-hash duplication
or native UUID aliasing; production checks were unchanged. Both the initial
refusal and final passing gate are retained. Existing mutation/drain, journal and
feature-finalizer regressions pass, as do ND, SWIM and upgrade/GNOI gates.

## Native qualification

Generated discovery **178** passed original-task drain, private exact cleanup,
owned completion proof and a separate unowned completion check against the same
native catalogue. Create task `01a14cd1-c5ba-74d8-b22d-d1d4ba5db4bd` and the exact
delete task recorded in the validation JSON were used; one create and one delete
were observed. Completion itself sent zero native mutations. The discovery
baseline and device inventory identities were preserved, and both finalizers
were retained. Deletion generation, Unknown faults, original accepted-task
reconstruction and the reserved finalizer were simulated in separate Kubernetes
state. No installed cluster/manager/worker was changed.

## Remaining integration

The reserved discovery finalizer is supplied manually only in qualification.
The common manager/registry currently has one optional lifecycle per adapter;
public discovery enrollment needs an additive way to support discovery while
preserving the existing feature finalizer and its recovery behavior. Worker
reconcile routing, manager metadata release and installed qualification remain
pending. Missing-source behavior, paused/controller-change policy, mixed/grouped
and legacy operation support and wider pinned-model gaps remain open.

Evidence: [source hashes and qualification records](discovery-cleanup-completion-validation.json),
[local completion](discovery-cleanup-completion-final-local.log),
[final API completion](discovery-cleanup-completion-api.log),
[initial API fixture refusal](discovery-cleanup-completion-api-initial.log),
[regressions](discovery-cleanup-completion-regression.log),
[ND/upgrades](discovery-cleanup-completion-nd-upgrades.log),
[47 SWIM functions](discovery-cleanup-completion-swim.log),
[controller/direct GNOI](discovery-cleanup-completion-gnoi.log),
[production build](discovery-cleanup-completion-build.log),
[native qualification](discovery-cleanup-completion-native.log).
