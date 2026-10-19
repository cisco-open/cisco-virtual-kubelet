# PnP workflow and configuration references

This adds another template consumer reader through the existing Catalyst Center
client, authentication, pagination and safe PnP inventory projection. It does not
enable public template pruning/deletion or PnP claiming.

## Collection and recovery

The reader binds the target template UUID, project and name, then includes its
base and every committed-version UUID as aliases. It inventories standalone PnP
workflow definitions and device main/system/reset workflow instances. Both root
`configId` and `tasks[].configInfo.configId` participate; repeated root/task IDs
produce one edge per consumer/configuration. Device
`workflowParameters.configList[].configId` also participates. Completed workflows
still hold references. UUID-looking text in CLI, parameters or output does not.

Native workflow definitions use a bare array with zero-based offsets and a maximum
500-entry page. Device pages retain the existing 100-entry boundary. A common
PnP page adapter reuses shared pagination; integer before/after collection counts
must equal the complete deduplicated result. Unavailable endpoints, malformed IDs,
wrong envelopes, repeating pages, short collections and changed counts return no
partial result. A declared device workflow ID that cannot resolve in the current
library is conservatively refused.

Native `id` and legacy/embedded `_id` workflow identities normalize to the same
internal identity before pagination. Conflicting aliases are refused. The safe
projection keeps only structural configuration IDs and existing bounded workflow
metadata: credentials, parameter values, URLs, CLI and output never enter the
reference result. Native task configuration details are documented in Cisco's
[SDK quickstart](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/quickstart/);
the shorter workflow endpoint schema does not show this required subtree.

Repeat the whole collection and compare sorted reference edges. Matching counts
alone cannot hide a newly added relevant reference. Independent API reads are not
an atomic snapshot and do not grant a mutation permit.

Private claim recovery now checks every present main-workflow template reference,
including task-only references. A foreign task ID cannot hide behind a matching
root or a pending image field. Matching IDs remain reference evidence, not native
configuration success, accepted-template version proof or onboarding completion.
System/reset instances cannot substitute for the main claim workflow. Existing
retained snapshots are not silently rebased when the expanded projection exposes
additional evidence.

## Qualification

- [115 focused race cases](template-pnp-reference-tests.log) pass in 4.036 seconds and cover identity aliases,
  paging/count completeness, base/version aliases, standalone and all three
  embedded instances, task-only and duplicate references, configuration-list
  bindings, malformed/late changes, safe projection and private claim consistency.
- [Broad configuration/template/profile/PnP regressions](template-pnp-reference-regression.log)
  pass in 172.093 seconds. The final private claim change is additionally covered
  by [all-PnP regressions](template-pnp-reference-final-pnp-regression.log), passing
  in 12.185 seconds, and the focused final-code run above.
- [Shared controller, ND and upgrade foundation checks](template-pnp-reference-foundations.log)
  pass; this change adds no transport, journal, CRD or worker protocol.
- [Initial corrected native cycle](template-pnp-reference-native.log) passes in
  143.94 seconds: generated project create/update, regular template create/update,
  two committed versions, two unassigned workflows, repeated reference counts
  0 → 1 → 2 → 1 → 0 and exact workflow/template/project cleanup. These are real
  Catalyst Center tasks and reference reads, not installed Kubernetes actors.
- [Final task-only native cycle](template-pnp-reference-task-only-native.log)
  passes in 139.74 seconds with the same complete lifecycle and exact cleanup.
  One workflow references the base at both root and task (one deduplicated edge);
  the other references a committed UUID only inside its task, with no root
  configuration UUID. Repeated collection finds both and returns to zero during
  cleanup. No physical PnP record or claim is used.
- Project cleanup now independently requires the authoritative global template
  inventory to contain no children of the captured project UUID: this appliance
  can omit `project.templates` even when children exist. The final
  [isolated project guard qualification](template-pnp-reference-project-cleanup-native.log)
  separately exercises real project create/update, the stronger guard and exact
  cleanup. This is a lab fixture guard, not public project deletion authority.
- The first minimal workflow request was rejected with HTTP 400 because its Config
  task lacked `configInfo`. A subsequent run detected references correctly but
  retained fixtures because response-time task defaults changed. Cleanup now
  compares declaration fields and independently requires Pending state, zero use
  and an empty counted PnP device inventory. The retained generated workflows were
  removed after exact identity checks; their two template/project pairs were
  removed after original NCTP Create-task UUID proofs, current identity/content,
  authoritative template inventory and zero profile/PnP consumer checks. See
  [workflow cleanup](template-pnp-reference-retained-workflows-cleanup.log) and
  [template/project cleanup](template-pnp-reference-retained-template-cleanup.log).
- [Pinned scope audit](template-pnp-reference-ledger.log) retains exactly 93 resource
  rows. It does not prove complete implementation parity.
- [Strict documentation build](template-pnp-reference-docs.log), formatting and
  whitespace checks accompany the code and coverage evidence.

## Remaining gates

Positive native device workflow instances, parameter-list bindings and physical
PnP claiming remain unqualified: the lab PnP device inventory is empty. No device
registration, claim, reset, upgrade or configuration deployment occurred in this
qualification. Active template deployment consumers and unsupported operational
variants, final destructive source/owner/private-state/version/reference guards,
public prune/delete/recreation, task/absence receipts and installed actor
qualification remain pending. The collector is a prerequisite, not a public
execution capability. Native template-version Delete remains state-only.
