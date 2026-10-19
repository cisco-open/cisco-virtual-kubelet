# IPAM omitted-key default-empty execution

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.

## Pinned semantics and implementation

NetAsCode 0.5.0 (`a435555fe828f03cf3311d52fe1ab6572a247916`),
`cc_network_settings.tf`, uses `try(local.catalyst_center.network_settings.ip_pools, [])`
for pool declarations and reservation mappings. An explicit `network_settings: {}`
therefore selects no allocations, just like `ip_pools: []`. This is a missing-key
fallback, not permission to coerce null, strings, unknown fields or unresolved
site references into an empty selection.

CVK now accepts both forms in the existing network intent parser, Retain Apply,
Report and pure-IPAM owned source pruning. Delete still requires the same stable
config/section owner and exact creation receipts. Retain leaves allocations and
receipts in place. The existing task/journal/readback/tombstone mechanisms are
unchanged. Zero desired resources does not bypass ownership or dependency checks.
A source revision remains distinct even when the two forms have the same meaning;
an already prepared operation cannot cross that revision fence.

Only an explicit selected `network_settings` object is accepted. This does not
relax the shared single-stripe parser or the selected-section presence contract.
Mixed-source pruning, whole-CR deletion, owner handoff, field removal and adoption
remain separate gaps. Upstream default resolution and required leaf schemas are
still the exporter’s responsibility.

## Fixture evidence

Focused parser/public-cycle checks pass (7.298s). The expanded prune safety run
passes (7.770s): both empty forms exercise owned child-before-parent deletion,
three known-task Unknown recoveries, tombstones, later explicit recreation with
fresh task/UUID proof, Retain/no-write, opaque lost acknowledgement, backend-in-use
refusal and source-change fencing. The final one-use dispatch test refuses a
new equivalent empty-source revision. Invalid null/unknown settings, unresolved
site profiles and dangling reservation references still fail compilation.

The later equivalent-empty transition check also passes (7.237s): changing
between explicit and omitted pools after verified cleanup leaves the retained
tombstones and causes no additional HTTP mutation. A separate public-reconciler
check passes (test 0.71s; package 2.649s) with an empty Delete source and existing
unowned allocations: no write, no receipt adoption, both pools and the reservation
retained, and the empty desired selection converges.

ND, the shared NaC document/model boundary and software-lifecycle race checks pass
(cached). The resource ledger was compared with every pinned `cc_*.tf` resource
declaration: 93 distinct upstream types, 93 distinct ledger rows, no missing/extra
types. This proves inventory coverage only, not implementation completion. The
complete Catalyst Center package passes with the race detector (617.494s;
`/tmp/cvk-ipam-default-empty-package.log`). The later equivalence and unowned
allocation tests were added during that run and passed separately as recorded
above; production parser/pruning changes were present throughout the full run.

## Native qualification

`TestLiveIPAMDefaultEmptyPruneManifest` passes (111.83s; package 113.976s), with
`CATC_IPAM_MANIFEST_DEFAULT_EMPTY=isolated-hierarchy`. The test created the isolated
`Global/CVK-Q-20261016-003444-000000000` hierarchy and used the public configuration
reconciler for dual-stack create/update, source revision to omitted pools, verified
pruning, later explicit recreation and a second omitted-pool prune. All 15
acknowledged native tasks were serialized, restored as Unknown and recovered with
no repeated submission. Final native pool/reservation baselines match their
original inventories, and the hierarchy was independently removed. No device
provisioning or upgrade was requested.

Private evidence directory:
`/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-ipam-dualstack-3332023736/`.
`complete.json` records `defaultEmpty=true`, `cleaned=true`, six POSTs, three PUTs,
six DELETEs and 15 known-task recoveries. Final `source.json` contains
`{"catalyst_center":{"network_settings":{}}}`; final results contain three verified
Delete tombstones. Captured source, operation, task, readback and receipt files
contain no authentication credentials. Native output:
`/tmp/cvk-ipam-default-empty-native.log`. Kubernetes persistence remains simulated
in the native harness; this is not installed-worker or device-service qualification.

| Config revision | Action | Native task |
| --- | --- | --- |
| 1 | Create | `01a14222-17c0-7597-a15b-bc1061447086` |
| 1 | Create | `01a14222-342e-72be-8d6e-0437714a5232` |
| 1 | Create | `01a14222-49ad-7064-9911-5fc65f34f985` |
| 2 | Update | `01a14222-6873-7e96-8d05-ec5a4cb4a3f8` |
| 2 | Update | `01a14222-7f4e-701b-9906-939c29756aba` |
| 3 | Update | `01a14222-9f20-791b-a63a-62b09d380e8e` |
| 4 | Delete | `01a14222-be69-755f-aa73-2fef178484fc` |
| 4 | Delete | `01a14222-d4f1-7b6b-ac56-4119ace4cbd2` |
| 4 | Delete | `01a14222-eb74-7ef3-b608-aae1effd39dd` |
| 5 | Create | `01a14223-091d-74f2-8a51-de6b637d1583` |
| 5 | Create | `01a14223-1f6a-74b4-a9aa-8ad6abc4ce98` |
| 5 | Create | `01a14223-3519-7bfe-8d0c-c2118b7588d5` |
| 6 | Delete | `01a14223-529e-7254-bc50-cd63287b269c` |
| 6 | Delete | `01a14223-693a-7e61-8482-b89bf80c3484` |
| 6 | Delete | `01a14223-81bc-7a99-83e8-aaec7cd32d93` |


## Lab availability and shared regressions

A separate read-only native run passes (package 4.409s): health/device/SWIM image
reads report three devices and two images. AP sequence selection rejects all three
wired identities before requesting mutation authority. Fabric inventory returns
no fabric sites, so multicast and embedded-WLC qualification is explicitly skipped,
not passed. These checks do not attest an upgrade or AP/fabric mutation. Output:
`/tmp/cvk-ipam-default-empty-lab-boundaries.log`.

The migration tool now describes the implemented IPAM owned pruning/default-empty
and discovery-only replacement boundaries rather than stale Retain-only/pending
text. Whole-section capability flags remain false. NaC model and migration-tool
race checks pass (migration tool 1.549s). Strict documentation build passes (5.06s);
`git diff --check` is clean.
