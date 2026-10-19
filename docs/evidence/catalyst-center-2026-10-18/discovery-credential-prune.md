# Credential-bearing discovery: terminal source pruning

Branch `pr/johalley/catc-support`; pinned NetAsCode module **0.5.0**.
This stage adds explicit source-prune execution and later recreation. It does
not establish native credential delivery, installed cleanup, config-deletion
finalization, source-loss recovery or full pinned-model parity.

## Lifecycle

`prunePolicy: Delete` permits cleanup of removed owned jobs; `Retain` preserves
them. Before selecting a write, the executor validates the entire declared model
and all removed creators. Exact native UUID/public-field proof is required; an
undeclared legacy v1 creator cannot invent its missing public fingerprint.

A terminal operation reuses the shared Delete permit, task timeout/Unknown
observation, repeated native absence and atomic retirement writer. It binds a
digest of the validated non-secret source, not a successor or secret values.
It has no credential versions and never writes a credential-delivery application.
Capacity admission reserves its retained action receipt before native deletion.
All original creator and credential records remain in the existing journal.

An empty desired list and empty Secret references remain on this typed lifecycle
when the config owns credential history. The ordinary executor cannot silently
drop that history or report cleanup through an unrelated path. Global-only jobs
in composed sources reuse the existing ordinary prune implementation and keys.

Recreation binds the terminal receipt and requires a later bound input, using
existing config-generation/ConfigMap-revision ordering. Historical credential
floors remain; an unchanged input cannot automatically recreate the job. Missing
history, lost permits, taskless results, failed tasks, identity drift and lost
retirement proof remain fenced without native replay.

## Qualification boundary

Controlled public reconciles and a real local Kubernetes API exercise partial
removal, an entirely empty source, ConfigMap changes, task timeout recovery,
Retain policy, active owned cleanup, a late conflict preventing partial cleanup,
legacy proof refusal, composed global-only pruning, lost permit/receipt
acknowledgements, pruned proof, taskless deletion, failed task and capacity.
Successful cases restore a later source and recreate exact new UUIDs with
retained credential history. Simulated restarts issue no additional writes.
The test credentials are markers; no device login is qualified by these cases.

The former blanket pruning-refusal test is updated to require unresolved input
to be refused before I/O under Delete policy. Authorized pruning is covered by
positive lifecycle cases rather than treated as unsupported.

Native device credentials are still needed for live credential-bearing discovery
and cleanup. No fake creator or terminal receipt is installed in the lab.
Config deletion/manager release, source-loss recovery, removal of the final
projected leaf from a retained job, family migration, metadata-only jobs and
broader resource-level parity remain open.

## Recorded gates

- Final discovery/shared-journal race and real-API regression: PASS,
  209.391 seconds, 1,535 passing test/subtest events.
- Twenty public prune scenarios pass with controlled native responses in both
  fake-client and real Kubernetes API persistence. This includes mixed ordinary
  pruning while an unrelated declared credential job is active.
- SWIM/XFSU/reload scoped regression: PASS, 2.864 seconds.
- Uncached shared contracts, ND, upgrade coordination and maintenance: PASS.

[Source/gate record](discovery-credential-prune.json),
[discovery regression](discovery-credential-prune-regression.log),
[SWIM regression](discovery-credential-prune-swim.log), and
[foundation regression](discovery-credential-prune-foundation.log).

Final review also qualifies orphaned creator/application refusal for empty sources.
Remaining credential applications or a recognized in-flight operation keep the
config on its typed lifecycle when action receipts are lost; incomplete pairs
never become ordinary empty-source success. These cases pass in both controlled
and real-API persistence.

Complete CVK build and strict documentation validation passed for the final
source. The [native read-only check](discovery-credential-prune-native-read.log)
also passed with three devices and two SWIM images. This verifies shared-client
connectivity, not credential delivery or native cleanup. The 93-resource ledger
remains intact and full pinned-model parity remains incomplete.
