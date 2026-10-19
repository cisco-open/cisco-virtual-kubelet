# Wireless definition task-tree completion

Date: 2026-10-16. Follow-up to
[acknowledged Update recovery](wireless-definition-recovery.md). The pinned model
remains NetAsCode 0.5.0; whole-resource parity is not enabled.

## Change

Accepted and Unknown Updates of declared anchor groups, interfaces, power
profiles, 802.11be profiles, pre-auth ACLs and RF profiles now use the existing
shared `configurationTaskTreeComplete` validator. The exact compiled selector
reuses the retained-task recovery shape and standalone/group namespace binding.

A successful root cannot complete a tree with a failed or pending child, a
foreign/disconnected branch, invalid time or malformed identity. An unavailable
tree remains fenced with no root-only fallback. The shared validator still owns
all tree validation; no second task parser or new transport/journal was added.
The selector checks a private phase projection for Accepted tasks without changing
the retained operation. The common reconciler still performs source/owner/generation
checks and independent UUID-bound desired-state readback before releasing work.

Creates, AP/SSID credential protocols and unrelated resources retain their existing
completion contracts. The mixed-plan credential/history fallback remains separate.
The feature-template result protocol is unchanged. No ND, SWIM or direct-device
foundation logic was changed.

## Verification

- [Definition fixture suite](wireless-definition-tree-tests.log): race pass
  12.340s. Sixty focused completion cases cover all six types in Accepted and
  Unknown, with complete, failed-child, pending-child, foreign-child and
  unavailable-tree observations. They assert exactly one tree read, no root
  fallback/mutation and no retained-phase mutation. Seventy-eight public
  reconciliation cases include child and endpoint failures, unchanged original
  source/target checks, taskless/Create refusal and no replay. The prior 276
  namespace/shape cases, combined six-collection cycle and SSID fallback remain.
- [Private Kubernetes API-server suite](wireless-definition-tree-api.log): race
  pass 24.052s. Forty-eight cases cover the six types with successful recovery,
  taskless/source/target fencing and child/endpoint failures. This uses actual
  CRD/status persistence and controlled native HTTP fixtures, not an installed
  cluster or device rollout.
- [Wireless and shared task completion regression](wireless-definition-tree-regression.log):
  race pass 97.667s. Production behavior is covered by the final focused suite;
  the broad run preceded the final defensive callback argument check and added
  focused completion cases. This is not a full CC-package run.
- [Native public cycles](wireless-definition-tree-native.log): all six pass in
  30.63s (package 33.012s). Each actual client/public reconciler flow creates a
  generated unused definition, serializes one acknowledged Update as Unknown,
  converges through native task-tree and independent readback, performs three
  no-op passes, then deletes only its captured fixture ID. Kubernetes persistence
  remains simulated in the native harness. Every case makes exactly one POST,
  one PUT and one DELETE; all six baselines as projected by the typed readers are
  restored. [Completion receipts](wireless-definition-tree-complete.json) retain
  all task/native IDs. There are no retained fixtures.

The native service returned singleton task trees for the observed definition
operations. Child-error semantics are qualified by controlled fixtures, not by
injecting failures into the appliance. Physical WLC/AP behavior and installed
actors remain unqualified.

Adapter registry, complete ND, shared software lifecycle and software-upgrade
packages also pass with race checking ([foundation log](wireless-definition-tree-foundations.log)).

## Create identity investigation

Read-only inspection of all six earlier generated Create tasks and their trees
found service `Common Setting Service`, generic successful progress, absent/null
`data`, and one root per tree. Those records contain neither created-object ID
nor a name-bound result. Independent metadata matching cannot provide the
feature-template protocol's task-returned UUID proof.

Consequently this change does not convert generic task success into durable
Create ownership or permit automatic prune/delete. That requires a separately
qualified native result/audit binding or an explicit identity-bound adoption
contract. Taskless submission recovery, adoption, pruning and complete
93-resource parity remain pending. Task-tree success alone also does not attest
device convergence.
