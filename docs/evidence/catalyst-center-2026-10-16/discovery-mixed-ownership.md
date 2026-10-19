# Discovery ownership in mixed tag/template manifests

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.
Pinned NetAsCode model: 0.5.0.

## Composition and ownership

The existing inventory-only tags/discovery group and combined template/inventory
compiler now bind discovery creation/replacement ownership through their existing
resource executors. Generic group ordering and whole-source validation are unchanged.
The inventory-only journal stays `inventory`; the combined journal stays `templates`
regardless of managed-section order. Grouped operations retain the existing
`discoveries/` prefix. Canonical operations and receipts are persisted unchanged;
only private native-verification copies are normalized to logical discovery scope.

The same native job name has one global result key, independent of journal grouping.
Adding templates to the same config can retain an original pure-discovery receipt
without recreating the job or rewriting its creation proof. A later replacement
uses original native ID/name/field-mask lineage, exact-ID deletion, successful task
and absence observation, then a fresh create/result receipt in the current canonical
namespace. This is same-config composition, not adoption or cross-config handoff.
Imports, foreign receipts, drifted IDs/fields and unverifiable legacy ownership
still cannot authorize replacement.

Binding reuses the existing shared journal, task observer, result publisher,
transport and one-use dispatch. Completion/recovery selectors intercept only the
bound discovery namespace and delegate prior observers, receipt writers and unknown-
task policies for unrelated actions. SSID and physical/deployment callbacks are
not replaced. Legacy acknowledged `discovery:` and `discoveries/discovery:` work
keeps its old executor/recovery path; completing it does not create new ownership.

The adapter-local shared dispatch/publication owner check now requires every
selected section, rejects a foreign owner in an overlapping section using the
existing overlap policy, and allows retained overlapping ownership by the same
config. Discovery and IPAM reuse this check. Missing sibling owners cannot be
hidden by the canonical journal section. Global logical duplicates are rejected
by planning, capacity reservation and verified-operation release, even when their
section/resource map keys differ in the Kubernetes status schema.

No public field, CRD/schema, provider, task loop, generic group or physical upgrade
foundation was added. Binding is limited to the qualified root and `discoveries/`
paths. Unqualified nested/private device compositions retain prior behavior.
Mixed-resource pruning remains unavailable: a global Delete policy must not
silently retain tags/templates that lack their own cleanup lifecycle.

## Fixture and Kubernetes evidence

The discovery/IPAM race suite passes with the final owner/duplicate guards
(16.525s; `/tmp/cvk-discovery-mixed-guard-final.log`). Public tests cover inventory
tags plus discovery, combined sections in both orders, three-stage acknowledged
Unknown recovery, same-config pure-to-mixed composition, result/operation retirement,
no-op and missing-receipt refusal. The initial existing combined test assumed no
ownership; its unowned-replacement fixture now explicitly removes the new receipt
before changing source. No ownership guard was relaxed.

Focused migration/legacy checks pass (3.181s). Removing the non-canonical inventory
owner, removing the canonical template owner, changing a selected/overlapping owner
or pruning the selected result refuses native dispatch. Same-owner retained overlap
remains valid. Duplicate logical results in distinct canonical namespaces cannot
reserve another write or release the original operation. Prior callbacks and
namespace refusals are separately checked.

Real isolated Kubernetes 1.35 Config/Controller CRD tests pass all four public
mixed scenarios, including composition migration (10.28s). Existing discovery
receipt/CAS/release and IPAM persistence checks also pass (package 24.234s).
A separate real-schema duplicate test passes (5.26s; package 7.175s): the API accepts
records for the same remote logical job in different section/resource keys, but
CVK retains the journal until that conflicting result is removed. Native task/API
responses are fixtures in these tests; this is not installed-worker qualification.

## Native mixed replacement qualification

`TestLiveDiscoveryMixedOwnership` passes (64.13s; package 66.267s), using the public
combined template/inventory reconciler, registered credentials and existing .101.
The generated discovery job `CVK_DISC_e4c25010d8927692` was created as native ID
`113`, replaced on an SSH to SSH,TELNET source revision, and recreated as ID `118`.
Both verified result records use canonical section `templates` and grouped
`discoveries/discovery-owned:` operations. All three public operations were
JSON-restored as Unknown and recovered without replay.

| Effect | Native task |
| --- | --- |
| Public create, ID 113 | `01a1426a-f171-7635-8aa3-2abfa5aec93d` |
| Public replacement delete, ID 113 | `01a1426b-4b88-764d-b5c8-6e0a9e84df23` |
| Public replacement create, ID 118 | `01a1426b-6632-7b16-b221-08f78bce9636` |
| Qualification-only exact cleanup, ID 118 | `01a1426b-dac3-791d-856b-d8e19a4e6188` |

The existing imported tag was selected only after a no-write tag preflight and
was unchanged in final metadata. The native request gate permitted only bounded
nonce-job discovery POST/DELETE requests; it allowed no tag mutation. This run
qualifies discovery execution composed with read-only tag convergence, not native
tag create/update in this same cycle. Tag mutations are covered by the public
HTTP fixtures/API-server scenarios above and their prior separate qualification.

Private evidence: `/private/tmp/cvk-discovery-owned-4277269818/`;
log: `/tmp/cvk-discovery-mixed-native.log`.
`mixed-complete.json` records cleaned/tagUnchanged true, canonical section templates,
two POSTs, two DELETEs including teardown, and three known-task recoveries.
`cleanup.json` retains the exact cleanup task and verified deletion. The original
job baseline and target device ID/serial/count were preserved. Kubernetes persistence
was simulated in this native run. No software upgrade, site assignment or template
deployment was requested; there are no generated jobs left to clean up.

## Final regression status and remaining work

The final complete Catalyst Center race suite passes (623.207s;
`/tmp/cvk-discovery-mixed-qualified-package.log`), with all production guard changes
and migration tests included. The later schema-only duplicate check is recorded
separately above. Two earlier broad runs were deliberately superseded after the
final receipt/overlap checks changed; their logs are not final validation results.
The repository examples pass compilation (2.303s). ND, gNOI, SWIM/software lifecycle,
model catalog and migration-tool race checks pass (migration tool 1.539s; other
packages cached). Focused Go formatting and `git diff --check` are clean; strict
documentation validation passes.

Mixed discovery/tag/template pruning, richer cross-kind progress, private/nested
physical paths, inline/per-site discovery, unverified-create cancellation, legacy
adoption, cross-config owner handoff, scale/capacity lifetime and installed actors
remain pending. These bounded results do not establish whole-section parity or
branch merge readiness.
