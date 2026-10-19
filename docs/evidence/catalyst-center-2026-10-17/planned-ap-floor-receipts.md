# Durable floor-related AP receipt format

## Storage and validation

The existing configuration action receipt now has an optional `effectReceipts`
set: at most 256 adapter-defined strings, each 1–512 characters. It records
bounded proof about related objects changed by the same native operation. It
does not introduce extra operations, tasks, a transport, a second journal or a
new NetAsCode input field. Legacy receipts omit the field.

For the pending Catalyst Center handoff, a distinct
`site-private-floor-update:v1:` producer binds the original site creator scope,
source/key identity, prior floor receipt digest and complete expected effect-set
root. The ordinary site latest-result format retains its existing native-state
MAC and appends that effect root for this producer only. Each AP effect encodes
its logical digest, exact observed AP UUID, scoped key identity, expected keyed
native-state MAC and prior-authority digest. No native payload or credential
value belongs in these records.

The effect root hashes public metadata and already-keyed fingerprints in a
canonical set order. Duplicate logical APs, duplicate native UUIDs, invalid
digests and over-limit sets are rejected. Each entry must resolve to an existing
private AP observation for the exact floor, owner, source mode, key identity and
compatible generations, established before the floor operation. A root mismatch,
missing member, missing producer/result root, absent anchor or changed binding
cannot settle the operation. Ordinary site Updates and site/AP creation receipts
cannot acquire related-effect scope.

Generated deep-copy code preserves independent effect slices. The atomic
receipt persistence helper now copies the complete expected receipt before its
status write and checks API readback. Site receipt decoding deep-copies related
metadata, and predecessor digests canonicalize set order. Legacy predecessor
digests retain their exact previous serialized contract when effects are absent.
The canonical and Helm NetworkController CRDs contain identical bounds.

## Current execution boundary

This phase implements the durable format, strict receipt decoding and API
compatibility protections. It does **not** enable the protected floor writer or
teach AP updates to consume a floor checkpoint. The existing geometry fence
remains, and the new producer cannot pass the existing ordinary Update admission
path. Tests assert that storage support alone cannot accidentally enable it.

The effect reader validates a retained record; it is not a substitute for native
verification. The remaining publisher must derive the complete expected effects
before dispatch, prove membership and predecessor authority, reserve count/byte
capacity, verify the original floor task and repeated full native state, then
publish operation and effects atomically. AP consumers still need explicit
current-floor checkpoint binding across multiple corrections and later floor
edits. These requirements remain in the [handoff contract](planned-ap-floor-effect.md).

No new native mutation flow is qualified by persistence tests. The earlier
single-AP width-resize calculation remains the native contract evidence; broader
dimension/units/version/multiple-AP behavior, full protected handoff, destructive
lifecycle, installed actors and complete pinned model parity remain open.

## Qualification

Real API tests seed synthetic already-verified native evidence; they never claim
that a Kubernetes round trip independently proves a native floor task or AP
effect.

- [Final real API qualification](planned-ap-floor-receipts-api.log) passes in
  14.696 seconds with Kubernetes 1.35.0 and repository CRDs. Twelve current-schema
  cases cover single/multiple AP proof, set reordering, missing members, missing
  or altered effects, pruned result/producer markers and schema rejection of
  empty, oversized, over-count and duplicate entries. Valid complete proof can
  settle; incomplete proof retains the operation. The fixture first skipped the
  original operation's API round trip; the corrected fixture mirrors the real
  publisher's API-normalized Verifying-to-Verified sequence without relaxing
  production comparisons.
- A thirteenth real-API case installs a copy of the older NetworkController
  schema without `effectReceipts`. Kubernetes actually prunes the submitted
  field. Atomic publication readback rejects the missing proof, and journal
  settlement remains fenced even though the API retained the Verified operation.
- [Schema/deep-copy/legacy group checks](planned-ap-floor-receipts-schema.log)
  pass in 1.971 seconds. Canonical/Helm schema equality, optional-field behavior,
  bounds, generated independent slices and unchanged group schema contracts are
  checked. [Generation log](planned-ap-floor-receipts-generate.log) records the
  existing controller-gen workflow.
- [Final site/AP/image/receipt/SWIM regressions](planned-ap-floor-receipts-regression.log)
  pass with the race detector in 76.557 seconds. Nineteen new receipt-boundary
  cases include AP/floor UUID collision, ownership, source/key/identity bindings,
  malformed/pruned records and copy independence. Additional set-root and
  predecessor-digest checks cover aliases, stable API set ordering, exact legacy
  digests and continued refusal of unimplemented floor-effect dispatch.
- [Shared controller, ND, lifecycle and direct-upgrade checks](planned-ap-floor-receipts-foundations.log)
  pass, partly cached. [Fifteen source/schema/test fingerprints](planned-ap-floor-receipts-sources.json)
  identify this phase. The resource ledger retains the same 93 unique pinned
  types; this is coverage accounting, not completion of all resource lifecycles.
- [Strict documentation validation](planned-ap-floor-receipts-docs.log) accompanies
  this phase. No new native handoff execution is claimed or enabled.
