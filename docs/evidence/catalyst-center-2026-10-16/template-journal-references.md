# Retained CVK template-deployment references

The reader observes references retained by one NetworkController's existing
configuration journal. It reuses the same regular/composite version-binding
parsers as deployment recovery, the shared immutable physical-operation contract,
the uncached controller reader and complete native template-version aliases. No
transport, CRD, status field, secret store, reservation protocol or RBAC grant is
introduced. This is a prerequisite observer; public prune/delete/recreation is
still unavailable.

## Reference contract

Include the current Prepared, Submitting, Accepted, Verifying, Verified or Unknown
operation. Taskless uncertain outcomes still reference their original templates.
Also include retained Verified action applications, separately labeled as
historical application references. A historical reference does not establish that
a native job is active or independently complete. An atomic Verified operation
and its identical application receipt can both be represented; conflicting copies
cannot. No native status is guessed from journal phase or a UUID.

Regular resource bindings retain their original base UUID, committed UUID and
version number. Composite dependency graphs retain the parent and ordered member
bindings, including version-only matches. Private-comparison extensions pass
through the existing parser, but key IDs, input/payload MACs, device serials,
source/config identities and parameter values do not enter the returned reference
projection. It contains journal class, operation/deployment UUID, template kind,
base/version UUID, version number and parent/member position only.

Validate every relevant retained binding before filtering for the requested
template: malformed unrelated deployment history cannot silently disappear.
Reject invalid dependency graphs, changed resource/section identities, unsupported
physical fields, contradictory phases/timestamps, missing accepted receipts,
non-deployment task IDs, duplicate application keys, conflicting operation copies
and deployment UUID reuse across operations. UUID reuse is checked canonically,
including case variants. Legacy missing application verification time is retained
as historical evidence, never promoted to completion.

Read source-independent retained state deliberately: editing/deleting a config or
removing a section owner must not erase an uncertain remote operation. Bind native
template name/project/base identity and all committed aliases, then repeat native
aliases and uncached controller observation. Journal/resource-version changes,
removed operations, new native versions, replaced targets and unavailable history
refuse the observation. This performs no status writes, journal retirement, lease
release or deployment dispatch.

## Qualification

- [64 focused race cases](template-journal-reference-tests.log) pass in 3.295
  seconds: retained phases, taskless/acknowledged Unknown outcomes, regular and
  composite parent/member/version bindings, private input projections, historical
  applications, atomic publication copies, malformed/duplicate/conflicting data,
  source removal and native/Kubernetes observation races. JSON reload models a
  fresh reader; it does not prove a native deployment happened.
- [12 private Kubernetes API cases](template-journal-reference-api.log) pass in
  9.260 seconds with the real CRDs and an isolated Kubernetes 1.35.0 API server.
  They exercise composite dependency persistence, config deletion/editing, owner
  removal, journal changes/retirement during reads and native-alias changes.
  Product HTTP is simulated. The installed cluster, manager and physical actors
  are not used.
- [Native template-history qualification](template-journal-reference-native.log)
  passes in 90.60 seconds. A generated project is created/updated; a regular
  template is created/updated and committed twice. Real native version/body reads
  bind a **simulated taskless Unknown CVK journal** before and after simulated
  config deletion and worker restart. No deployment UUID or device success is
  manufactured. Exact template and project cleanup is independently verified.
- [Broad template/configuration/PnP/profile regressions](template-journal-reference-regression.log)
  pass in 173.166 seconds; [shared controller/ND/software-upgrade foundations](template-journal-reference-foundations.log)
  pass. Parser result exposure changes no existing wire identity or receipt.
- [Strict documentation build](template-journal-reference-docs.log), formatting,
  whitespace and the [93-resource scope audit](template-journal-reference-ledger.log)
  accompany this implementation. Scope rows do not establish full parity.

## Coverage boundary and next requirements

An empty result proves only that the observed local journal has no matching known
binding. It does not establish appliance-wide deployment absence. Other
NetworkControllers/namespaces, endpoint aliases, independent API/UI users,
scheduled jobs and native history are outside this reader. Namespace endpoint
ownership does not establish exclusive appliance ownership across these scopes.
A deleted or pruned journal cannot reconstruct missing external operations.

The inspected [official Cisco SDK's 2.3.7.9 template module](https://github.com/cisco-en-programmability/catalystcentersdk/blob/main/catalystcentersdk/api/v2_3_7_9/configuration_templates.py)
provides deployment submission and status by a known deployment UUID; no complete
deployment enumeration contract was identified in that module. The
[recorded API-surface inspection](template-journal-reference-api-surface.json)
includes the retrieved source hash and exact routes. This is not proof that no
other appliance API exists. Do not invent a list endpoint or interpret an empty
local journal, an unavailable status query, or a generic task list as global idle.

Full operational reference protection still needs a qualified complete native
activity/scheduling contract and coverage of other controller identities. Actual
physical deployment, native outcome recovery and installed actors remain
unqualified. Public destructive execution additionally needs combined profile,
composite, PnP and deployment checks, fresh private/version ownership, final
source/owner/journal guards, ordered child/project deletion, task/absence receipts
and safe recreation. Those requirements remain part of the original parity goal.
