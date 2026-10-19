# Guarded template and device tag association submission

## Changes

Both membership executors now freeze consumed identity/tag selectors before
planning. Template payload/content and composite-member slices are not retained
for this observer. Device tag slices and declarations cannot be changed through
caller-owned input after planning.

The existing shared association planner requires a reobservation callback and
performs two complete fresh observations before POST. Tag/member UUID, protection,
presence, declared status and later selections must match the original plan.
Template observations additionally carry the native project UUID in memory.
Invalid native IDs are refused. A changed or already-completed selection requires
fresh reconciliation, and every attempted closure is consumed on success,
refusal, observation error or uncertain/missing acknowledgement.

The native request remains an additive single-member POST through the existing
transport. Unrelated memberships do not change the selected observation and are
not included in the request. No shared journal, reservation, actor, CRD or other
infrastructure provider was changed. Existing dispatch authority and durable
uncertainty handling remain required; this guard does not grant authority or
clear a retained journal on its own.

## Reproduced race

The first implementation refreshed the selection once. A test changed tag
protection while that refresh was reading member rows, after its earlier tag
catalog read. Both template and device cases incorrectly reached POST. See
`membership-late-before-fix.log` for the deliberately failing reproduction.

The final implementation repeats the full observation and refuses the changed
protection state without POST. Both cases pass along with replacement, later
selection, existing-membership, reader-error, input-mutation and no-replay cases.
A native project replacement with the same template UUID is also refused within
a retained in-memory plan. The existing journal format has not gained a durable
project-incarnation anchor; that broader recovery guarantee is not claimed.

## Final native qualification

- Mixed device membership: positive dynamic evaluation, manual promotion to MIXED,
  STATIC persistence after rule change, restored MIXED, exact manual removal back
  to DYNAMIC, retargeting to empty, clear and exact tag cleanup all passed.
  Original unrelated tag metadata was restored.
- Template membership: isolated project create/update, draft/commit operations,
  tag creation and guarded association/readback/no-op passed. Association, tag,
  template and project cleanup were verified. No device template deployment ran.

See `membership-guard-mixed-native.log` and
`membership-guard-template-native.log`. These are native compiled-executor tests,
not installed Kubernetes actor qualification. Earlier intermediate native runs
were repeated after the second-observation fix; only final runs are linked here.

## Regression scope

`membership-guard-local.log` records final tag/device/template membership race
checks. `membership-guard-affected.log` covers affected tag, device membership,
template, inventory, discovery, AP, extranet and SWIM tests on the final revision.
Strict documentation and whitespace checks passed. No new full-suite run or ND/
direct-provider implementation change is claimed.

Remaining work includes installed/grouped lifecycle, durable dependency graph
recovery, pruning/protected-tag operations and broader concurrency/scale cases.
Overall NetAsCode parity remains incomplete.

Subsequent qualification closes the in-flight template project-incarnation gap
using the existing operation resource field, without widening the physical
dependency graph. See [project binding and legacy recovery](template-tag-project-binding.md)
for the updated implementation, serialized journal fixtures, native qualification
and remaining installed/legacy-repair limits.
