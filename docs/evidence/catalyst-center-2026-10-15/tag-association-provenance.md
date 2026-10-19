# Dynamic evaluation and explicit tag association provenance

## Production change

The existing paginated member reader now supports explicit DYNAMIC, STATIC and
MIXED filters using the documented `memberAssociationType` query parameter.
Unfiltered callers keep their previous query. No new transport, authentication,
retry policy, journal, actor or infrastructure provider was introduced.

The [Cisco member API](https://developer.cisco.com/docs/catalyst-center/get-tag-members-by-id/)
distinguishes rule-derived, manual and combined associations. Previously the
explicit device-tag executor accepted an unfiltered matching UUID, allowing a
rule-derived match to satisfy a manual declaration. That could disappear when
the rule changed. Explicit device tags now require the union of STATIC and MIXED
membership evidence. DYNAMIC-only presence leaves the manual assignment pending.
A failed provenance query supplies no fallback unfiltered presence. Native member
identities must be valid API identifiers.

## Positive native evaluation

A new opt-in mode of the existing isolated tag harness pins two actual wired
inventory identities and repeatedly checks UUID/serial/hostname/address/platform
bindings and hostname uniqueness. It creates a random user-defined tag and
qualifies EQ predicates selecting:

1. The first switch, with exactly its UUID under DYNAMIC.
2. The second switch, with the old UUID removed and exactly the new UUID present.
3. A checked nonmatching predicate, with no members.
4. An explicitly cleared definition, still with no members.

STATIC, MIXED and template memberships remain empty in this pure-dynamic cycle.
Native definition tasks/readback/no-op, unrelated tag metadata and final exact-UUID
cleanup/baseline restoration were verified. See `tag-positive-native.log`.
This qualifies EQ evaluation/retarget removal, not every operator’s evaluation or
clearing directly from a matching population.

## Manual/dynamic coexistence

An optional extension uses the existing device-membership executor on the same
isolated tag. The lab verified:

| Action | Observed provenance |
| --- | --- |
| Initial matching EQ predicate | DYNAMIC only |
| Explicit manual assignment of that same device | MIXED only |
| Predicate changed to a checked nonmatching hostname | STATIC only; explicit verification and no-op still pass |
| Original predicate restored | MIXED only |
| Only the owned manual association removed | DYNAMIC only; the rule match remains |

The actual API treated these as distinct classes in this run. Unfiltered presence
remained exactly the selected device throughout coexistence. All changes used
captured tag/device UUIDs, completed tasks and independent class/readback checks.
Foreign memberships, changed identities, changed tag definitions or unrelated tag
metadata fence qualification. The original definition and pure dynamic state were
restored before the parent harness retargeted to empty, cleared the definition,
and deleted its tag. No manual fixture association remained.

See `tag-mixed-native.log`. No device configuration, template deployment or
installed Kubernetes workflow was executed; these are native metadata/executor
qualifications. Existing manager/worker physical admission remains unchanged.

## Tests

- Filter query/type/tag scope persists through two pages and 501 identities.
- Unfiltered query layout is unchanged.
- DYNAMIC-only presence requires a manual write; STATIC/MIXED presence converges.
- Successful manual addition verifies and does not replay.
- Unknown association selectors and failed filtered queries cannot supply evidence
  or silently fall back; malformed member IDs cannot leave usable partial evidence.
- Existing device-membership, tag, template, inventory, discovery, AP, extranet
  and SWIM race regressions passed in `tag-provenance-affected.log`
  (104.890 seconds). Final invalid-scope/member-ID cases are recorded in
  `tag-provenance-final-local.log`. Strict documentation and whitespace checks passed.

## Remaining scope

Other operators’ positive evaluation, interface rules, direct clearing of a
matching population, broader provenance races/transitions, public membership
pruning and tag deletion, system-tag mutation and installed actor qualification
remain pending. Overall NetAsCode parity remains incomplete.
