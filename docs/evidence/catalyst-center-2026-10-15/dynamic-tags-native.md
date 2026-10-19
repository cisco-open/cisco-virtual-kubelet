# Dynamic tag lifecycle and native submission revalidation

## Architecture and behavior

The existing parser, shared tag executor, transport, configuration journal,
section ownership and combined inventory/templates precedence remain in use.
No new protocol, store or infrastructure provider was introduced. ND, gNOI and
SWIM implementations were not changed.

Tag writes now freeze the complete desired payload, including nested rules, and
reobserve all declared tags immediately before submission. Replaced UUIDs,
create-name races, changed protection state, changed preserved rules and changes
to later declarations refuse the stale plan. Unrelated tags remain independent.
The same pure planner builds initial and fresh requests, including preserved
native fields; exact request identity and serialized body must still agree.
Any attempt consumes the closure, including refusal or uncertain acknowledgement.
This does not replace the shared durable journal's uncertainty/recovery rules.

## Native qualification

The [Cisco Tag API](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/create-tag/)
describes networkdevice rules with logical operations and nested item conditions.
Two isolated runs exercised two-condition AND rules with EQ hostname/family leaves:

- Standalone templates compiler: create and rule/description update on the same
  UUID, accepted-task completion, independent readback and fresh-compiler no-op.
- Combined inventory/templates compiler: the same lifecycle with inventory
  precedence and one effective tag in combined reporting.

Both used random user-owned tag names and hostname predicates checked against the
actual complete device inventory. Networkdevice and template memberships remained
empty. Before mutations and cleanup, exact fixture UUID/protection and unchanged
unrelated tag inventory were checked. Both fixtures were deleted by their captured
UUIDs after verified completion, and the complete original tag baseline was
restored. No device assignment/configuration or template deployment occurred.

See `dynamic-tags-native-templates.log` and `dynamic-tags-native-combined.log`.
These are native compiled-executor qualifications; installed Kubernetes workers
were not used in these runs.

## Discovered appliance constraint

The first isolated request used a one-condition AND. Its task failed terminally
with `InternalError`, reporting `Index 1 out of bounds for length 1`; subsequent
complete tag inventory contained no fixture. The failed operation was not replayed.
Fresh, separate fixtures with two conditions passed. The failure/task/absence
observation is preserved in `dynamic-tags-single-condition.json`, and the initial
failed test log is retained for context.

The first implementation rejected AND groups with fewer than two conditions.
Subsequent direct-EQ and unary-source qualification superseded that restriction:
the compiler now lowers unambiguous single-condition AND/OR inputs to their atomic
condition. Empty/ambiguous groups remain refused. See
[the later operator qualification](dynamic-tag-operators.md).

## Local tests

`TestTagPlansRevalidateNativeState` covers create races, replacement, protection
flips, externally changed preserved rules, late protected declarations, unrelated
changes, scalar/nested caller mutation and missing acknowledgements without replay.
The original single-condition rejection test was superseded by unary logical
normalization and empty/ambiguous-group tests during the later operator work.

Combined static and dynamic public reconciliation fixtures exercise both section
orders, restart, one effective mutation, atomic ownership, terminal journal clear,
resource counts and current-generation InSync without repeating an unchanged
source generation.

`dynamic-tags-affected.log` covers tag, template, inventory, discovery, AP,
extranet and SWIM race tests on the stale-plan implementation (101.369 seconds).
The final AND-constraint and dynamic public-restart additions are covered by
`dynamic-tags-final.log` (3.248 seconds). No new full-suite or installed qualification
is claimed. Strict documentation and whitespace checks also passed.

## Example and remaining scope

`examples/configs/catalyst-center/dynamic-tags-apply.yaml` uses the pinned 0.5.0
resolved model and Retain policies. It was parsed but not applied to the lab.

Other operators/rule forms, interface rules, positive matching/evaluation,
empty-rule clearing/removal, system-tag mutation and installed-cluster execution
remain pending. This qualifies a bounded dynamic-tag lifecycle and does not mark
overall NetAsCode parity complete.
