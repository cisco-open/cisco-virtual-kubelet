# Counted template-to-profile reference observation

The template reference reader now reuses the authenticated Catalyst Center client,
shared list decoder and existing switching-profile inventory/detail readers. It
adds no transport, controller interface, CRD, Secret access or worker protocol.
This is one reference class needed by the remaining destructive template lifecycle,
not a public prune implementation or complete consumer graph.

## Collection contract

For a base template UUID, read the reverse profile count, the unpaged profile
list, then the count again. Require matching counts and exactly that many unique,
valid profile IDs. Missing, null, fractional, negative or excessive counts,
malformed lists, duplicate/missing IDs, HTTP partial/error responses and changed
counts return no partial reference result. A zero count cannot substitute for a
valid list response. The collection is bounded to 10,000 entries, consistent with
the existing native profile reader's bounds.

The [documented reverse API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/retrieve-the-network-profiles-attached-to-a-cli-template/)
has a template-ID path parameter and no offset/limit parameters. The reader does
not invent paging behavior or infer completeness from the common 500-item page
size. A fixture with 600 entries tests the unpaged/count contract; this does not
claim native qualification at that scale. The nonempty lab probe independently
verified the actual id/name item shape and the expected legacy-created profile UUID.

Cross-check the independent legacy switching-profile detail collection for both
cli.templates and day0.templates. Preserve both kinds as references and retain
modern-only or legacy-only positive evidence. Unknown attribute/member schemas,
duplicate bindings, detail errors or changed profile identity refuse collection.
Opaque inherited site.settings is skipped as a known separate settings class:
its contents are never interpreted, fingerprinted, logged or returned here.
The reader returns only profile IDs and reference kinds, not profile names,
attribute values or native objects.

Repeat the entire combined observation and compare sorted reference identities.
Equal counts with changed IDs or a late legacy reference do not form a stable
view. These separate reads are not an atomic native snapshot. A future destructive
caller must still establish target identity/current private state, every other
consumer class, source/owner/journal authority and final pre-submit checks. The
reader alone grants no mutation or deletion capability. Existing Retain execution
and its compatibility paths are unchanged.

## Qualification

- [Focused race tests](template-profile-reference-tests.log): 35 cases cover
  complete empty/single/large responses, malformed/missing counts and lists,
  duplicates, partial/error responses, target validation before API access,
  Day-N/onboarding/modern-only/legacy-only references, unknown schemas, identity
  changes and late references. Private names/values and opaque inherited settings
  do not enter the returned reference snapshot.
- [Nonempty native discovery](template-profile-reference-probe-native.log) uses
  the existing isolated project/template/profile/site workflow with public profile
  reconciliation, then verifies the reverse response has one item/count and its id
  matches the captured legacy profile UUID. This is schema discovery, not a Delete
  permit. [Structural artifact](template-profile-reference-shape.json) retains only
  field names/counts and which fields match the known test identity.

- [Native reference lifecycle](template-profile-reference-native.log) passes in
  284.27 seconds. Eight stable observations cover an initially empty template,
  empty profile creation, Day-N attachment, site-assigned Day-N retention, removal,
  onboarding attachment/removal and restored Day-N attachment. Both native views
  observe the positive Day-N and onboarding bindings. Public profile reconciliation
  rebuilds between phases, verifies InSync and checks no replay. Exact generated
  profile/site/template/project cleanup passes. Native APIs/tasks are real;
  Kubernetes persistence and worker restarts are simulated. No device deployment
  occurs. This qualifies the profile class only, not complete removal safety.
- [Configuration/template/wireless/network-profile race regressions](template-profile-reference-regression.log)
  and [shared ND/direct/SWIM foundations](template-profile-reference-foundations.log)
  pass.

- [Strict documentation build](template-profile-reference-docs.log), whitespace
  checks and the [93-resource scope audit](template-profile-reference-ledger.log)
  pass. The scope audit is a backlog check, not a full capability claim.

## Remaining work

Composite drafts and all committed-member references, PnP onboarding workflow
bindings, active deployment dependencies and other unsupported consumer variants
still need complete collection and qualification. Template source pruning,
Delete-task/absence receipts, ordered child/project removal, recreation and
installed manager/worker execution remain pending. The pinned provider's version
Delete is state-only; no native version deletion is added. Full NetAsCode parity
remains unfinished.

The subsequent [composite consumer reader](template-composite-references.md) adds
drafts and all committed versions as a separate reference class. The profile
reader remains unchanged; operational consumers and public pruning remain pending.
