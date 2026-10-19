# Discovery config cleanup: private execution and recovery

The deletion-bound Prepared operation now advances through the existing one-use
submission CAS, exact owned discovery delete, original-task observation, native
absence verification, atomic ownership receipt and journal settlement. This is a
**private lifecycle**, with no public reconciler/finalizer enrollment or manager
owner release.

A restart reconstructs the spec/deletion/evidence envelope and source binding from
the persisted operation. Only exact Prepared work can request dispatch authority.
The original create receipt and whole owned cohort must still match. Final native
preflight and the existing owned-dispatch guard recheck identities and retained
evidence after the write-ahead record is persisted. Expanded finalization resource
identities participate directly in existing receipt capacity checks.

Accepted and supported known-task Unknown work observes only its original task;
missing acknowledgements or missing task IDs remain fenced. Verifying reuses the
owned discovery verifier: correlated completed task, repeated exact UUID/name
absence reads and atomic Verified receipt. Settlement requires the exact receipt
and another native absence check; it clears only the operation slot and retains
the inventory owner and deletion tombstone. Invalid or pruned envelopes retain
the generic verification/settlement fence.

## Controlled and API validation

Sixteen execution scenarios cover the normal lifecycle, injected known-task
recovery, lost acknowledgement, backend error, native drift, receipt/spec edits,
last-moment owner/native races after the submission permit, running/failed/wrong
original tasks, pending absence, a reused name/foreign UUID, missing completion
receipt and malformed envelope. Generic malformed settlement is checked separately.
The same execution scenarios run against real Kubernetes CRDs, alongside existing
18-mode observation and preparation refusal tests. Discovery, shared journal and
wireless feature deletion regressions pass separately. ND, software-upgrade, SWIM
and direct-device/controller upgrade gates are recorded below.

## Qualification boundaries

The native lifecycle uses one generated discovery for the existing .101 lab
switch, with a transport allowing exactly one owned delete. The deletion request,
restart and Accepted-to-Unknown fault are simulated in a separate Kubernetes API;
no installed cluster is changed. Native task and absence checks use Catalyst Center.
The final dedicated execution qualifier passed on job **168**: original create
task `01a14cbf-6553-7c28-8237-4525294f2308`, exact delete task
`01a14cbf-c41a-72b8-87eb-71b74a3b333d`. One create and one delete were observed;
the discovery baseline and lab inventory identities were preserved. The initial
private lifecycle qualification also passed independently on job163.

Public manager enrollment/finalizer completion remains pending. Original work
accepted before the deletion request needs a separate drain bridge. Missing sources,
empty/unowned enrollment, paused/changed-controller observation policy, installed
qualification and wider NetAsCode gaps remain open. This does not establish full
93-resource execution parity.

Evidence: [source/qualification record](discovery-cleanup-execution-validation.json),
[controlled tests](discovery-cleanup-execution-tests.log),
[API tests](discovery-cleanup-execution-api.log),
[regressions](discovery-cleanup-execution-regression.log),
[ND/software upgrades](discovery-cleanup-execution-nd-upgrades.log),
[47 non-live SWIM tests](discovery-cleanup-execution-swim.log),
[final native lifecycle](discovery-cleanup-execution-native.log),
[initial native lifecycle](discovery-cleanup-execution-native-initial.log),
[controller/direct GNOI gates](discovery-cleanup-execution-gnoi.log),
[production build](discovery-cleanup-execution-build.log).
