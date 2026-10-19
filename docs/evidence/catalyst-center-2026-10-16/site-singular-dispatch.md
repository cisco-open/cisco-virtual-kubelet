# Singular site dispatch and certified no-dispatch recovery

## Implementation

Area, building and floor singular Create/Update now use the existing one-use
configuration dispatcher to repeat native observations immediately before sending.
The comparison binds the complete authored site intent, exact planned method,
endpoint and payload, and the typed target/ancestor chain through Global. Target
presence, UUID, kind, parent identity and preserved typed attributes are significant.
Unrelated sites and collection ordering are excluded. Duplicate identities, missing
ancestors, broken ancestry and incomplete responses refuse dispatch. This is a
typed Site projection, not a promise to compare every unknown native field.

The public compiler attaches the existing source/owner/journal dispatch guard
between two fresh native observations. It checks controller identity/generation,
unpaused state, config/source identity and revision, current Submitting operation
and section ownership. There is no new transport, credential path or public schema.
Bulk, floor-image and planned-AP execution keep their existing distinct paths.

A refusal raised before invoking the original sender carries the existing private,
one-use, guard-bound capability. Only matching singular metadata operations can
retire through the existing CAS journal flow and fresh requeue. Owners and retained
history remain unchanged. Neither an HTTP error after dispatch nor matching native
state proves that no write occurred. Lost acknowledgements remain Unknown and are
never replayed. Deletion, root/bulk resources, physical reservations, credentials,
prepared operations and acknowledged tasks do not enter this retirement path.

## Verification

- [99 focused race subcases](site-singular-focused.log): 69 sender cases across
  area/building/floor creates and building/floor updates, plus 30 public restarted
  reconciliation cases. These cover mutable intent, target appearance/replacement,
  ancestry and selected/unselected typed attributes, changes during authority
  checking, source/owner changes, partial reads, unrelated rows and lost replies.
  The focused set passes in 11.153 seconds.
- [17 authorization scope cases](site-singular-scope.log) pass in 2.361 seconds.
- [20 real private Kubernetes API cases](site-singular-api.log) use Kubernetes
  1.35.0 and real CRDs, with simulated product HTTP. They pass in 27.628 seconds.
  This proves private API persistence; it does not qualify installed workers.
- On source revision change the reconciler can discard stale-source status instead
  of publishing NativePreflightRefused. The test checks the old operation retired,
  no write occurred and the section owner stayed unchanged; it does not require a
  status update based on a stale source. Foreign-owner edits retain the fence.
- [Native basic cycle](site-singular-native.log): one full three-object source
  creates an isolated area/building/floor hierarchy, then changes floor width.
  Task trees, native readback, restarted InSync/no-op and exact cleanup pass.
- [Native injected-refusal cycle](site-singular-native-replan.log): four locally
  injected pre-submit 503 read failures cause four public retirements, followed by
  exactly three creates and one update. Owners/history remain intact. Full native
  readback and exact child-first cleanup pass in 35.02 seconds. These are local
  fault injections, not appliance outages. Kubernetes persistence and worker
  restarts are simulated; native HTTP requests and tasks are real.
- [Creator-result follow-up](site-singular-creator-result-native.log) repeats that
  native cycle and additionally reads every original completed create task before
  cleanup. Its canonical result UUID identifies the exact expected native kind,
  path and parent for each of the three sites. It passes in 35.38 seconds including
  cleanup. This establishes a bounded native identity contract; it does not issue
  or persist a production creator receipt or enable public deletion.
- [Site/floor/no-dispatch regression](site-singular-regression.log) passes in
  32.955 seconds; [broader configuration/template/site/floor regression](site-singular-broad.log)
  passes in 179.001 seconds. [ND/controller/software lifecycle and direct upgrade
  packages](site-singular-foundations.log) pass (some cached).

Reproduce the native follow-up using the existing live test runner with credentials
loaded privately and these explicit opt-ins:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_SINGULAR_MANIFEST=isolated-hierarchy
CATC_SITE_NO_DISPATCH=isolated-preflight-reads
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterSiteSingularManifest$' -count=1 -v
```

The runner also needs the existing CATC endpoint, username and password environment
variables; never print them. No physical device or SWIM operation is performed.

[Source hashes](site-singular-sources.json), [exact 93-resource inventory audit](site-singular-ledger.log)
and [strict documentation build](site-singular-docs.log) accompany this work.
The 93 types include 91 Catalyst Center resources plus terraform_data/time_sleep;
counting resources is scope accounting, not proof of feature parity.

## Remaining work

Public site prune/delete is still unsupported. It needs durable creator lineage,
private latest-applied baselines, complete dependency/consumer protection and
atomic lifecycle settlement. Native task-result evidence alone grants none of
those permissions. Singular production task completion still uses the existing
root-task observer; complete-tree enforcement and acknowledged Unknown recovery
need separate implementation and public/native qualification. Installed actors,
real reservation lifecycle, hardware behavior and the other ledger gaps remain
open. Full pinned-model support is not complete.
