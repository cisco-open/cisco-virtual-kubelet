# Dynamic tag operators and unary logical compatibility

Later [pagination and matching-clear qualification](tag-pagination-and-matching-clear.md)
adds positive two-condition AND/EQ and unary AND/OR evaluation, plus direct
clearing/restoration of matching populations. Historical limits below describe
this earlier operator-only qualification.

Subsequent [association provenance qualification](tag-association-provenance.md)
adds positive EQ evaluation and explicit/manual coexistence; the operator-only
scope recorded below remains unchanged.

## Native qualification

The existing isolated tag harness and compiler/executor passed independent
user-defined networkdevice tag create/update cycles for:

| Source form | Native qualification |
| --- | --- |
| Two-condition OR with EQ hostname leaves | Same-UUID rule update, readback/no-op, empty memberships and cleanup |
| Two-condition AND with LIKE hostname and EQ family leaves | Same lifecycle |
| Two-condition AND with ILIKE hostname and EQ family leaves | Same lifecycle |
| Direct IN hostname with two `values` | Same lifecycle |
| Direct EQ hostname with one `value` | Same lifecycle |
| One-condition AND/EQ, then OR/EQ | Lowered atomic requests; same lifecycle |

Each run used a new random fixture name, checked nonmatching predicates against
complete native inventory, confirmed zero networkdevice/template memberships,
preserved unrelated tag metadata and deleted only its captured fixture UUID after
verified completion. All original tag baselines were restored. No device
configuration or template deployment occurred. See the accompanying
`dynamic-tag-operator-*-native.log` files and `dynamic-tag-unary-native.log`.

These qualify definition persistence/lifecycle, not positive predicate evaluation,
case-sensitivity effects, membership migration or arbitrary grammar combinations.
Unary EQ forms were native-qualified; other atomic unary leaves are locally
transformation-tested rather than claimed as separate native qualifications.

## Source compatibility and validation

The [Cisco API rule schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/create-tag/)
specifies alternative `value` and `values` selectors. The parser now rejects both
being present before any earlier tag can write. Multi-value request tests verify
that the array is mapped intact and frozen against caller mutation.

Earlier raw unary AND requests failed on the appliance with an internal indexing
error and left no tag. After direct EQ qualification, the compiler now lowers an
unambiguous one-item AND/OR to its atomic name/operator/value condition. This
preserves single-condition logical meaning without submitting the failing native
wrapper. Empty groups, ambiguous parent selectors and incomplete/logical child
conditions cannot silently become a different request. Existing multi-item
expressions are preserved. No transport, journal or provider foundation changed.

## Public recovery and tests

The public unary inventory fixture exercises create, changed predicate update,
explicit clear, restoration and an equivalent AND-to-OR source generation.
Reconciler restarts retain the same native UUID and complete the existing journal.
All generations reach current-generation InSync; the equivalent fifth generation
adds no write. Exactly four native mutations occur in the fake API fixture.

Local tests cover unary AND/OR normalization for atomic EQ/LIKE/ILIKE/IN string
leaves, exact field/value preservation, source immutability, empty/ambiguous groups,
selector conflicts in both tag sections and frozen value arrays. Native IN
qualification used the root multi-value form, not a scalar IN unary leaf.

See `dynamic-tag-operators-local.log` and `dynamic-tag-operators-affected.log`.
The latter covers affected tag/template/inventory/discovery/AP/extranet/SWIM race
tests (104.984 seconds); the focused final comparison/public tests passed in
4.280 seconds. Strict documentation and whitespace checks passed. This is not
a new full-suite or installed-worker qualification.

## Examples and remaining scope

`examples/configs/catalyst-center/dynamic-tag-operators.yaml` illustrates the
qualified definition forms. It was parsed, not applied; native tests used only
random isolated fixtures.

Broader combinations, nesting/cardinality boundaries, interface rules, positive
evaluation and removal of previously matched members, public tag deletion/prune,
system-tag mutation and installed-cluster execution remain pending. Overall
NetAsCode parity remains incomplete.
