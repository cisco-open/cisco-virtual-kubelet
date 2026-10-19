# Wireless tag scope conflict validation

Pinned model: Catalyst Center NetAsCode 0.5.0. Scope validation is adapter-local
and reuses the existing whole-model preflight and before-submission observation
path. No new provider, transport, journal, physical reservation or actor protocol
was introduced.

## Native contract and implementation

Cisco's documented
[site-tag create API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/create-multiple-site-tags-for-a-wireless-profile-in-bulk/)
states that only one site tag can be saved per site ID, with later definitions
overriding earlier ones. Its
[policy-tag create API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/create-multiple-policy-tags-for-a-wireless-profile-in-bulk/)
describes overrides for matching zone selections on the same or a parent site.
CVK must not use submission order to overwrite another declaration or native tag.

Each planned payload is now compared with every other desired tag in the same
profile and every existing native tag of that kind, including tags omitted from
the source. Site-tag conflicts use exact site identities; parent/child site-tag
assignments are distinct. Policy comparisons also recognize ancestor/descendant
paths using complete authoritative site inventory and slash-delimited hierarchy,
not string-prefix coincidence. Shared policy scope is qualified only for disjoint
zone sets. Empty zone sets mean the native built-in default-zone. Partial zone
intersection is conservatively refused; the documentation's different-set wording
alone does not qualify partially intersecting assignments.

Unknown native fields and unresolved/aliased site identities refuse observation.
Existing same-name tags retain their ordinary UUID and membership checks.
Whole-source conflicts fence earlier profile/assignment prerequisites. The same
observer runs again before submission, so a newly observed foreign conflict
prevents the POST/PUT. Public Apply returns ConflictingWirelessTags with an
explicit scope message and no new journal operation. No deletion, migration,
adoption or automatic tag replacement is added.

## Verification

- [Focused race checks](wireless-tag-scope-focused.log): PASS, 3.847s.
- [All wireless/network-profile race fixtures](wireless-tag-scope-wireless.log):
  PASS, 8.770s, including existing manifest/restart and retained-reference flows.
- [Complete ND/shared foundation package suites](wireless-tag-scope-foundations.log):
  PASS (cached). These run without the name filter.
- [Selected shared/manager checks](wireless-tag-scope-manager.log): PASS;
  manager/connector/RBAC package 3.993s. ND/managed-protocol entries in this
  filtered run had no matching tests; their complete suites are linked above.

Fixtures cover duplicate source scope, existing foreign native tags, a foreign
conflict introduced after planning, unrelated sites, same-site disjoint policy
zones, partial intersection, both hierarchy directions, Global ancestry,
similar-looking unrelated paths, default-zone equivalence and missing site IDs.
Public source-conflict fixtures verify actionable failure, zero native mutation
and no operation journal. Unmodeled foreign metadata remains fenced.

## Remaining qualification

Native checks use only isolated profile definitions, disabled SSIDs and empty
areas. They do not deliberately submit an overwrite. Hardware/WLC behavior,
ancestor-site native fixture coverage, partially intersecting policy zone
semantics, custom-zone combinations, membership removal/migration/adoption and
installed Kubernetes actor qualification remain pending. CVK cluster recovery
still awaits separate approval; the approved private backup remains intact.

## Native result

The [fresh native cycle](wireless-tag-scope-native.log) passed in **88.175s**,
test body **86.29s**. Both tag kinds completed create and same-ID additive
membership updates, retained readback without replay, no-op convergence and
drift reporting. After each stage the compiler refused a second overlapping
site-tag declaration and a second overlapping default-zone policy declaration;
public native inventories were unchanged. No conflicting native POST was sent.
Exact captured tag, assignment, profile, disabled-SSID and empty-area identities
were removed and all captured public baselines restored. No device assignment,
provisioning or physical operation was submitted.
