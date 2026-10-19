# Discovery cleanup after ConfigMap loss or change

New Delete-policy pure-discovery ConfigMap configs record a typed non-secret
`DiscoveryCleanupScope` condition before any mutation. It binds config/controller
UIDs and generations, complete spec, source UID/version and model digest. Source
parsing remains strict. A new source scope is not recorded while any journal is
occupied; fresh idle-controller reads and the ordinary publication snapshot guard
prevent a stale scope write racing accepted work.

Only deleting configs can use that retained scope. Their references and subject
must still match; the existing original task, native UUID, strong field mask and
ownership receipts remain authoritative. New active intent cannot execute from a
missing source. Later unapplied ConfigMap edits/replacement do not substitute new
intent into deletion. The original accepted Create is observed through its real
task/UUID and shared strong-field verifier, without adoption or replay. Cleanup
uses the existing immutable operation, exact delete, absence receipt and manager
proof/release. The completion proof binds the retained scope digest as well.

## Validation

Twenty-three public scenarios include source removal after application or while a
create is accepted, replacement/edits, pruned/forged scope, spec changes, native
drift and missing acknowledgements. Unavailable legacy sources without a retained
scope remain fenced. The same cases and a scope-publication/journal race pass with
real Kubernetes CRD status CAS and manager patches; manager cache indexes are
emulated. Existing discovery, shared journal, feature deletion, ND, upgrade, GNOI
and all 47 non-live SWIM test functions pass their selected/full-package gates.

Live job **198** used a manager-enrolled ConfigMap source. After creation, its real
original task was reconstructed as outstanding Unknown in separate simulated
Kubernetes state, and the ConfigMap was removed. The active worker sent no new
intent. Public deletion recovered the original task/UUID without the source,
performed one exact delete, retained the receipt and released the manager finalizer.
One create/one delete occurred; native discovery baseline and inventory identities
were preserved. Kubernetes source loss and task faults were injected; no installed
process was changed. The initial verifier extraction compile failure occurred
before any lab mutation and is retained separately.

## Broader audit and repairs

The older full public-lifecycle audit completed in 1678.746 seconds, with 13,536
passing test/subtest events and failures only in example compilation and site-bulk
families. It is a failure, not a final current-tree pass. It reported example
compiler setup failures and a real site-bulk action binding bug.
The example fixture now models enrollment, owned prune and the private comparison
compiler rather than treating those as generic unbound plans. Bulk creation now
passes its `Create` action to the unchanged shared mutation validator. Focused
example and bulk tests pass, including no-replay/uncertainty behavior. This is
controlled bulk qualification; no live bulk-site mutation was performed.

The full retained-source/site-bulk race audit completed successfully in 1676.277
seconds, with 13,561 passing test/subtest events and no failures. Its
[terminal evidence](discovery-pre-handoff-full-suite.json) predates the later
ownership handoff additions and is not a post-handoff full-suite claim. Root CI
uses the same explicit 40-minute package budget; its smoke job allows 75 minutes.
The nested Terraform job is unchanged. Sixteen release-contract tests and pinned
actionlint pass; hosted CI remains unqualified. Subsequent installed manager/worker qualification
passed a naturally timed-out original task, source deletion, worker restart, exact cleanup
and manager release without status injection; see [installed evidence](discovery-installed-source-recovery.md).
Automatic owner retirement/handoff, unattested legacy source loss,
unsupported spec/controller changes and wider pinned-model parity remain pending.

Evidence: [source/native/gates](discovery-retained-source-validation.json),
[public cases](discovery-retained-source-tests.log),
[API/race](discovery-retained-source-api-final.log),
[discovery regressions](discovery-retained-source-regression.log),
[final native run](discovery-retained-source-native-final.log),
[initial native compile failure](discovery-retained-source-native-build-initial.log),
[ND/upgrades/GNOI](discovery-retained-source-nd-upgrades-gnoi.log),
[47 SWIM functions](discovery-retained-source-swim.log),
[example/site-bulk repairs](discovery-retained-source-suite-repairs.log),
[production build](discovery-retained-source-build.log),
[older full-suite snapshot](discovery-retained-source-baseline-progress.jsonl),
[workflow contract](discovery-retained-source-workflow-contract.log),
[pinned actionlint](discovery-retained-source-actionlint.log).
