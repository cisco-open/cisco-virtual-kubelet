# Manual template tag association evidence

The pinned NetAsCode 0.5.0 `cc_templates.tf` resource
`catalystcenter_assign_templates_to_tag` supplies explicit template UUIDs to a tag.
CVK must observe the manual association; unfiltered rule-derived presence alone
does not establish that effect.

Cisco's [2.3.7.5 tag member API](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/get-tag-members-by-id/)
documents STATIC for manual association, DYNAMIC for rule-derived association
and MIXED for both. The lab supports these query forms for template members.

## Implementation

Device and template explicit assignments now share one type-scoped reader that
unions complete paginated STATIC and MIXED inventories. Template planning,
reporting and verification use that evidence. Failure of either query returns
no usable evidence; there is no unfiltered fallback. Unfiltered readers remain
available for inventory and exact-fixture cleanup.

The existing project-bound operation key, template/tag UUIDs, whole-source
pre-submit guards and one-use dispatch remain unchanged. No model field, schema,
transport, credential store or journal lifecycle was added. Recovery still makes
two observations of only the acknowledged association. Each now makes two
classified membership reads, for four membership requests total, independent of
unrelated declared tags.

## Validation

The focused race suite passed in 4.092 seconds. The final broader template/tag,
journal, global credential and SWIM race suite passed in 63.714 seconds. Coverage
includes dynamic-only planning/verification refusal, transition to manual-plus-
dynamic evidence, STATIC/MIXED no-op, 501-member filtered pagination for both
types, invalid scopes, failed-query refusal and failure after partial STATIC
evidence. Project-bound journal recovery and bounded reads remain tested.

A read-only native existing-tag probe accepted all four template query forms;
its empty results establish query support, not positive rule evaluation.

The isolated project/template/tag lifecycle passed in 33.03 seconds (package
35.147 seconds). After the manual POST, independent queries verified exactly the
test template in unfiltered and STATIC results, with empty DYNAMIC and MIXED
results. Serialized operation identity, fresh-compiler verification and no-op
passed. Exact association/tag/template/project cleanup was verified.

Captured UUIDs:

- Project: `76017a7a-7250-4645-bc27-6d97aab0d4fa`.
- Template: `3934545a-b4cf-4bf6-91d8-145c52f531b4`.
- Tag: `1bcc4b51-9e48-47f4-a8f6-14c735a4e8ad`.

Complete task and cleanup receipts are in `template-provenance-native.log`.

## Remaining scope

Positive template DYNAMIC/MIXED rule transitions are fixture-qualified only;
the native run qualified manual STATIC association. Native device coexistence
qualification does not prove positive template rule evaluation. Installed
workers/API-server lifecycle, pruning, protected-tag mutation and evidence-based
legacy repair remain pending. No template was deployed to a device. Full
NetAsCode parity remains incomplete.
