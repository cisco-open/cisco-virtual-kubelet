# Protected floor changes publish AP checkpoints

## Execution

The existing sites executor now supports an explicitly enabled floor/AP handoff:
`cisco.vk/catalyst-center-floor-ap-handoff: "true"`. It requires the existing
private AP opt-in and projected site comparison key. Configurations without this
annotation keep the previous pre-dispatch floor fence; there is no automatic
adoption, baseline migration or public destructive lifecycle.

Before a floor Update, CVK reads the complete native floor-local AP inventory.
Every native member must match a retained private AP identity on that exact
floor, including APs omitted from the current source. Missing, replaced, renamed
or unowned members refuse before mutation. Current private baselines and exact
predecessor authority are checked, then complete expected native AP effects are
calculated and bound in the existing floor operation resource.

The resource-specific preflight repeats expected effects around the existing
source/owner/journal guards. Explicit opt-in is reread before submission and
verification. The normal site sender, transport, one-use no-dispatch capability,
full original task-tree recovery and floor UUID result verification are reused.
No synthetic AP operation or AP task is created for the floor's side effects.

After the original floor task completes, the publisher verifies the desired
floor and original task, reads the complete AP effects twice and requires exact
agreement with the prebound root. Source/key/identity/owner checks surround
publication. Verified and the related-effect set are stored atomically in one
bounded floor latest receipt, with API readback verification. The existing
[AP consumers](planned-ap-floor-consumers.md) then issue independently journaled
corrections to the declared AP positions using that floor operation identity.

Capacity checks reserve both the floor effect metadata and worst-case AP latest
envelopes before mutation. Existing AP latest rows are replaced in the size
projection, not double-counted; their possible format/task-size growth is still
reserved. This calculation changes no pending proof or persisted state. Count
and byte exhaustion must not strand corrections after an otherwise successful
floor update. Reservation is conservative for retained APs outside current
source; source-specific reductions are not used to weaken the bound.

## Native precision contract

The lab returns floor dimensions in feet as binary32 shortest decimals, even
when the requested dimensions are in metres. Expected geometry first models
that observable representation without rewriting the requested payload.
Changed AP axes use explicit binary32 operand, division and multiplication
rounding before shortest-decimal output. Other AP fields remain exact and
unmodified; private baseline comparisons never acquire an epsilon or blanket
rounding.

Two native diagnostic runs exposed distinct problems in the earlier model:
request-versus-readback dimension representation, and intermediate rather than
final-only coordinate rounding. The first resized fixture exposed X=`6.0850325`;
the next completed one handoff but withheld the second checkpoint at
X=`6.2857146`. Neither failed operation's prebound root was rewritten or replayed.
The original tasks and full trees were independently verified complete, then
only the exact generated AP/site identities were removed using original create
result UUIDs, parent paths and empty-child readback. Cleanup of both diagnostic
fixtures is recorded separately. This supersedes the earlier calculation-only
phase's narrower precision evidence.

## Operator model

Use `examples/configs/catalyst-center/planned-ap-floor-handoff.yaml` as an
alternative exclusive sites-owning configuration with a new hierarchy/AP. It
uses the same NetAsCode 0.5.0 schema, secret projection and Retain policies as the
existing private AP example. After initial InSync, change only floor width from
12.654 to 14 and then 16, retaining the declared AP coordinates. Each resize
should show the floor task, verified related effects, AP correction task and
InSync. Source/opt-in changes before dispatch cause refusal; uncertain task or
effect outcomes remain fenced rather than trigger automatic replay.

## Qualification

- [Native cycle](planned-ap-floor-handoff-native.log) passes in 225.28 seconds
  including exact AP and hierarchy cleanup. Starting from a newly created
  private hierarchy/AP, two source width changes publish original-task floor
  effect checkpoints and trigger same-UUID AP corrections. Complete native AP
  state, radio UUIDs and the original observed anchor remain unchanged after
  each correction. Eight design writes (two images, AP create/edit, two floor
  resizes and two corrections), ten acknowledged-task recoveries, seven bounded
  receipt rows and three stable no-ops are counted. No fixture remains from
  this run.
- [Initial diagnostic](planned-ap-floor-handoff-diagnostic-initial.log) and
  [intermediate diagnostic](planned-ap-floor-handoff-diagnostic-intermediate.log)
  retained their mismatched prebound operation fences. [Known-outcome cleanup](planned-ap-floor-handoff-diagnostic-cleanups.log)
  records exact AP/site removal after independent original-task/create-result
  checks. No physical device operation or unrelated retained fixture was touched.
- [48 real Kubernetes API cases](planned-ap-floor-handoff-api.log) pass in
  105.127 seconds: nine automatic handoff cases, five existing consumer cases,
  twelve keyed baselines, nine type updates, twelve effect receipt cases and one
  actual old-schema pruning case. Handoff coverage includes two APs, ConfigMap/
  image composition, original floor/AP task recovery, unowned members, opt-out,
  ignored/unrelated effects and missing task receipts.
- A subsequent metadata-only refinement reserves worst-case growth even when
  replacing existing AP latest rows. [Final nine handoff API cases](planned-ap-floor-handoff-api-final-headroom.log)
  pass in 34.755 seconds, including inline/ConfigMap and two-AP recovery. Native
  mutation, task/effect verification and consumer logic did not change in that
  refinement. The final [affected regressions](planned-ap-floor-handoff-regression.log)
  pass with the race detector in 90.144 seconds. Dedicated count, byte,
  correction-headroom and existing-row replacement cases also prove the
  reservation leaves current state and the pending proof unchanged.
- [Shared controller, ND, lifecycle and direct-upgrade tests](planned-ap-floor-handoff-foundations.log)
  pass, partly cached. [Four operator example contracts](planned-ap-floor-handoff-examples.log)
  pass separately after adding the explicit opt-in assertion.
- [Sixteen current source/example fingerprints](planned-ap-floor-handoff-sources.json)
  and [strict documentation validation](planned-ap-floor-handoff-docs.log)
  accompany this phase. The ledger retains the same 93 unique pinned resource
  types; this closes a handoff lifecycle gap, not full model parity.

Product HTTP in public/private API fixtures is simulated. Native qualification
uses the real appliance/task APIs and production reconciliation, with simulated
Kubernetes persistence and worker restarts. It is virtual map design, not
physical AP/WLC provisioning, a switch upgrade or installed-worker qualification.

To reproduce the native case, use the existing private credential runner with
`TestLiveCatalystCenterPrivateSiteDesign` and these explicit test flags:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_PRIVATE_BASELINE=isolated-design
CATC_AP_NATIVE_BASELINE=isolated-keyed
CATC_AP_FLOOR_HANDOFF=isolated-original-task-effects
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_LIVE_REQUESTS_PER_SECOND=2
CATC_LIVE_TEST_TIMEOUT=20m
```

The handoff variant uses a bounded 24-step stage verifier and a 15-minute native
context. An observation timeout never authorizes resubmission or baseline
rewriting. Credentials remain private and are not included in the model/logs.

## Remaining scope

Full pinned model parity remains incomplete. The handoff needs broader native
qualification for multiple APs, models/radio counts, length/height/unit changes,
boundary rounding and other controller versions. Current root verification must
stay strict for unqualified native effects. Bulk site mode, public destruction,
exclusive AP creator/adoption policy, installed actors and other resource-level
gaps remain tracked separately in the 93-resource ledger.
