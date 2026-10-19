# Versioned transitive identity proofs for wireless tags

New private tag operations now use `tag-proof:v4` envelopes with explicit
`write`, `members` and `delete` modes. The existing approved `/network_profiles`
comparison-key projection, journal, original-task/tree recovery, bounded action
ledger and atomic writer are reused. No new controller, CRD, manifest field,
secret channel or persistence store is introduced.

## Binding and lifecycle behavior

Private site-tag proofs retain their existing direct AP-profile UUID bindings.
Private policy-tag proofs additionally bind the complete native parent AP-zone
graph: parent UUID, zone names and membership, RF-name-to-UUID mappings and
Global SSID-name-to-UUID mappings. This includes parent zones outside the
individual tag's selection, so a same-name dependency replacement cannot hide
behind an unchanged tag payload. RF configuration and SSID credentials are not
copied into this graph. Graph values stay in memory; journal selectors and
private effects contain only digests/MACs and existing public identities.

New write selectors commit to the graph before dispatch. Final preflight repeats
both the ordinary native/source checks and the bound snapshots. Recovery and
readback reconstruct the exact committed selector; private publication brackets
the original task/tree proof with stable native/binding observations. Each bulk
producer set remains complete, and latest-slot replacement preserves original
Creator/sibling evidence. Read-only attribute verification can reconstruct the
public digest without a comparison key; private publication still requires the
approved key, ownership and source/CAS checks.

Nonempty membership reduction retains the existing affected-subtree consumer
checks. For whole-tag deletion, the v4 scope seal also commits to the complete
parent dependency graph. That graph can be reobserved after the tag itself is
absent, preventing an RF/SSID replacement between deletion and tombstone
publication from being accepted. All v4 envelopes retain the existing
512-character bound; insufficient scope/receipt capacity refuses dispatch.

The full parent context is deliberately part of the proof. A later legitimate
parent dependency change may require ordered lifecycle/handoff work; it does
not silently rebase a settled proof. Wider parent/config deletion and retirement
remain pending.

## Existing journals

V1 writes, v2 reductions and v3 deletions retain their original decoding,
comparison and recovery semantics. Existing MACs are not retroactively treated
as containing RF/SSID UUID history. An unchanged legacy object stays legacy;
only a new verified Update can publish a stronger v4 latest baseline for the
same managed UUID. Imported matches remain unowned. Pruning preserves the
retained proof class: a v4 baseline cannot be downgraded to a v3 deletion, and
legacy history cannot be silently upgraded by changing the deletion envelope.

## Qualification

The [case-counted validation inventory](wireless-tag-bindings-validation.json)
records **140 local and 140 actual temporary Kubernetes API cases**: 34 membership,
60 whole-tag pruning and 46 receipt cases on each surface.

The [combined local](wireless-tag-bindings-combined-local.log) and
[combined API](wireless-tag-bindings-combined-api.log) commands each completed
their membership/pruning families, then failed in the old receipt fixture's
`producer-changed` expectation. V4 rejects the changed producer while still
Unknown, before the fixture expected Verifying. The test now permits that earlier
fence while still requiring retained original work, no replay, no receipt and
no InSync. **Neither failed aggregate is presented as a passing gate.** Their
terminal passing families are combined with the complete corrected
[46-case local](wireless-tag-bindings-receipts-local.log) and
[46-case API](wireless-tag-bindings-receipts-api.log) receipt runs.

Additional passing checks:

- [17 dependency fault/readback cases](wireless-tag-bindings-readback.log):
  selected/unselected RF and SSID replacement, missing references, ordering
  stability, pre-delete custody and post-delete absence proofs, and modular
  private-executor verification.
- [Envelope and custody checks](wireless-tag-bindings-protocol.log): all operation
  modes/actions, malformed/future scopes, single-producer deletion and the
  legacy/v4 deletion-class matrix.
- [Genuine legacy journal compatibility](wireless-tag-bindings-legacy.log) and
  [six initial lifecycle cases](wireless-tag-bindings-positive.log). These are
  focused subsets, not additional unique cases beyond the full families above.
- [Live-harness compilation and session renewal](wireless-tag-bindings-live-build.log)
  and [SWIM compatibility](wireless-tag-bindings-swim.log).
- [Complete-source ordering regression](wireless-tag-bindings-dependencies-local.log):
  28 lifecycle cases and nine projection checks locally (69.415 s), plus the same
  [28 lifecycle cases through temporary Kubernetes APIs](wireless-tag-bindings-dependencies-api.log)
  (173.284 s). These retain actual-reference requirements, foreign/unknown
  reference fences and no-replay behavior with v4 membership/prune selection.

[Current source fingerprints](wireless-tag-bindings-sources.json) describe this
implementation; they are not installed-binary attestations. Earlier milestone
inventories retain their historical qualification-time versions. The final
modular verification adapter branch is independently covered by the readback
gate; the corrected receipt runs use the final fixture expectation.

The binding structs now declare their existing canonical JSON names explicitly.
The [wire contract](wireless-tag-bindings-wire-contract.log) checks exact bytes
for populated and empty graphs (2.382 s), preserving the original v4 encoding
without a protocol change. This prevents a future Go-field rename from silently
invalidating receipts. The [pre-annotation source inventory](wireless-tag-bindings-sources-before-wire-names.json)
is retained for the native compilation boundary; annotations change no encoded
field names, order or zero-value behavior.

## Native qualification passed

The [complete serial native run](wireless-tag-bindings-native.log) passed both
single-tag and custom-zone cohort workflows (869.44 s and 1703.20 s; Go aggregate
2574.968 s). The [independent audit](wireless-tag-bindings-native-audit.json)
verifies **31 distinct original task IDs**, five membership reductions, five tag
deletions and four association removals. Each workflow reduces membership before
its first association removal and deletes tags before its final association
removal. Complete producer digests, exact affected site UUIDs, nine retained
private rows/ten member proofs, final no-op and restored baselines are verified.

These workflows use generated disabled SSIDs, unused profiles and empty areas.
No device assignment, provisioning or device operation was submitted. Kubernetes
actors in the native harness are simulated; temporary API qualification above
separately checks persistence. The earlier [single-workflow segment](wireless-tag-bindings-native-single.log)
and [partial audit](wireless-tag-bindings-native-partial.json) remain historical
intermediate evidence; the complete run and audit above supersede their pending
status.

The earlier v3 whole-tag and identity-collector native passes remain historical
evidence, not a substitute for the new v4 lifecycle run. Installed workers,
physical wireless behavior, recreation/migration, adoption/handoff/retirement,
broader dependency ordering and the remaining 93-resource gaps remain open.
