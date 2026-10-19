# Composite draft and historical-version references

The composite consumer reader reuses the existing authenticated client, complete
native template inventory, draft reader and full version-footprint reader. No
transport, CRD, provider interface, secret store or worker protocol is introduced.
It is one consumer-class prerequisite for safe template removal, not a public
prune path or complete operational reference graph.

## Collection

Bind the target's original base UUID, parent project UUID and name to the current
native inventory. Collect every draft and every committed version of every native
definition, including other projects. The target alias set contains its base UUID
and all its committed-version UUIDs, so version-pinned members are not overlooked.
Global base/version identity collisions and repeated version identities refuse
collection. Existing version readers validate native parent/name/version bindings
and complete version metadata before membership is examined.

Inspect structural containingTemplates member IDs only. UUID-looking strings in
body content or parameter values do not become references. Explicit regular
objects with no member collection are supported. Composites require an actual
member array; missing kind/collection, null or malformed members, duplicate IDs,
unknown regular-object memberships and failed reads return no partial result.
References retain only consumer template UUID, consumer version UUID (empty for
a draft), and the matched target member UUID. Bodies, parameters and names are
never returned as reference data or published into status.

Repeat the complete observation and compare sorted identities. Late draft or
old-version references, changed target-version aliases and disappearing consumer
objects cannot produce a stable view. Separate API reads are not an atomic
snapshot: a destructive caller still needs current private-state/version ownership,
all other consumer classes and final source/owner/journal/native dispatch guards.
No new Delete permission or automatic cleanup policy is enabled here.

## Qualification

- [Focused race cases](template-composite-reference-tests.log): 19 cases cover
  complete empty, draft/base, old/current committed and cross-project references;
  opaque content that happens to contain UUID text; target replacement; unknown/
  missing/null/duplicate member schemas; unavailable or foreign-parent history;
  same-size draft edits, late old-version references, new target aliases and
  removed consumers. Only reference identities leave the reader.

- [Native isolated history cycle](template-composite-reference-native.log) passes
  in 491.05 seconds. The initial composite draft and two committed versions all
  reference the original member. A generated alternate member is then created and
  committed; the composite draft is updated and a third version committed against
  that alternative. Complete repeated observation still finds the original two
  committed-only references, while the alternate has draft/latest references.
  Cleanup removes the composite before the alternate and original members, then
  verifies exact project absence. Native APIs, committed versions and task roots
  are real. This is native compiler/reference-reader qualification, not installed
  Kubernetes reconciliation, new ownership qualification or device deployment.
- [Configuration/template/wireless/network-profile race regressions](template-composite-reference-regression.log)
  and [shared ND/direct/SWIM foundations](template-composite-reference-foundations.log)
  pass.
- [Strict documentation build](template-composite-reference-docs.log), whitespace
  checks and the [93-resource scope audit](template-composite-reference-ledger.log)
  pass. The inventory check confirms no missing/extra rows, not full parity.

## Remaining gates

PnP workflow/configuration references, active deployment dependencies and other
unsupported operational consumer variants remain pending. Public source pruning,
ordered project/child deletion, Delete-task/absence receipts, recreation and
installed manager/worker qualification are also unfinished. The pinned version
Delete remains state-only; no native version-delete API is invented.


Follow-up: [PnP reference collection](template-pnp-references.md) now implements
that consumer-class reader and qualifies unassigned native workflows. Physical
PnP instances and public destructive lifecycle remain pending.
