# Dynamic tag rule clearing and restoration

Later [association provenance qualification](tag-association-provenance.md) adds
positive EQ evaluation and retarget removal. The clear/restore runs below used
empty populations and do not establish direct clearing of matching populations.

## Implementation

The existing tag parser, executor, native transport and shared configuration
journal remain in use. Report, planning and verification now use a tag-specific
comparison helper that treats native null/omitted and empty rule lists as equal
only when the desired model explicitly selects `dynamic_rules: []`.

Omitting the field leaves rule ownership unchanged. The comparison does not
modify desired or observed objects, and request construction still preserves
existing unselected rules. It does not add a clearing field to updates of static
tags whose native response has no rule list. A nonempty native rule list cannot
match an explicit empty selection, and a nonempty desired list cannot match
missing native rules.

## Native cycle

An opt-in extension of the existing isolated live harness passed:

1. Create a randomly named user-defined tag with a nonmatching two-condition
   networkdevice AND/EQ rule.
2. Update the hostname predicate and description while retaining the native UUID.
3. Explicitly clear the rule list and independently observe zero native rules.
4. Restore a rule definition and independently observe one rule entry again.

Every step completed its accepted task and passed independent native verification
and fresh-compiler no-op convergence. Native rule-entry counts were 1/1/0/1.
Networkdevice/template memberships stayed empty, unrelated tag inventory stayed
unchanged, and cleanup deleted the captured fixture UUID and restored the exact
original tag baseline. No device configuration or template deployment occurred.
See `dynamic-tag-clear-native.log`. A second complete cycle through the combined
inventory/templates compiler also passed and restored its baseline; see
`dynamic-tag-clear-combined-native.log`. That run explicitly observed a nil
native rule slice after clearing (`nilList true`), with non-nil rule lists
before clearing and after restoration. This confirms why literal empty-array
comparison alone would fail convergence on the appliance.

This proves definition clearing/restoration on an empty fixture, not removal of
previously evaluated member assignments or positive dynamic-rule evaluation.

## Public and negative fixtures

`TestDynamicTagClearRestorePublicRestartCycle` exercises an inventory manifest
through create, rule update, explicit clear, restoration and unchanged fifth
source generation. It restarts the reconciler between observations, requires
current-generation InSync and terminal journal clearance, preserves the native
UUID, and counts exactly four native mutations. Its clear readback uses null
rather than an empty list.

The initial fixture accidentally reloaded the stored sites selection before
persisting its inventory selection. The setup now sets the selected section after
the refresh; no production authority or supported-source guard was weakened.

`TestTagEmptyRuleComparisonIsSelectionScoped` checks null/empty/present native
rules against selected and omitted intent, including desired-object immutability.
`TestTagUnmanagedRulesArePreserved` checks actual update request bodies for static
and dynamic native tags with omitted rule intent.

See `dynamic-tag-clear-final.log` for focused public/comparison tests and
`dynamic-tag-clear-affected.log` for the final broader affected race regression run (102.675 seconds).
No installed Kubernetes or full-suite qualification is claimed.

## Examples and remaining scope

`examples/configs/catalyst-center/dynamic-tags-clear.yaml` updates the same config
object as `dynamic-tags-apply.yaml` and retains the tag itself. It was parsed but
not applied; only isolated random fixtures were used for native qualification.

Other rule forms/operators, interface rules, positive evaluation and removal of
previously matched members, public tag deletion/prune, system-tag mutation and
installed-cluster behavior remain pending. Overall NetAsCode parity is incomplete.
