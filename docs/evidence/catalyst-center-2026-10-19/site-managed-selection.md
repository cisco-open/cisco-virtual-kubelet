# Managed-site source selection qualification

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`; branch
`pr/johalley/catc-support`. Full model parity remains **incomplete**.

## Execution and custody

The existing sites owner consumes a versioned source envelope containing the
complete resolved model and selection policy. Only its private execution view is
filtered. The existing ConfigMap UID/resource version or inline generation binds
both parts; the existing shared owner, journal, transport, original-task recovery,
private comparison key and destructive lifecycle checks remain authoritative.
There is no new owner, lease or transport implementation. Strict older workers
refuse the selected envelope rather than silently applying the whole model.

The shared pinned selector supports literal prefixes, exact sites and Global-only
selection. All declarations and assets validate before filtering. Excluded creator,
AP and image custody stays retained. Prune Delete is limited to selected omitted
owned targets and existing dependency/identity proofs. Exact mode does not invent
ancestors. A genuine in-scope image omission still uses the existing source-bound
omission/reintroduction protocol.

The exporter emits one complete private ConfigMap, preserving the asset index and
BinaryData. Provenance distinguishes full and selected counts and artifact/model
digests. Re-export preserves scope; raw-model output refuses to discard it.
See [usage](../../controllers/catalyst-center-site-defaults.md).

## Verification

- Focused race tests cover selector modes, malformed/unknown/null input,
  excluded declaration validation, custody retention, selected prune/recreate,
  source freshness, selected image/AP recovery and genuine image omission.
- Real temporary Kubernetes API tests cover persisted hierarchy custody,
  selected private image/AP recovery and image omission/reintroduction fences.
- A fresh native generated hierarchy completed four setup mutations, six design
  writes including two synchronous image uploads, and eight original acknowledged
  task recoveries. Floor-only selection followed by empty Global scope and
  re-selection under Delete policy caused no additional writes and preserved the
  entire custody journal. Exact AP cleanup preceded hierarchy cleanup; the typed
  site inventory baseline was restored with no active operation or fixture left.
  Native Kubernetes persistence is simulated; installed selection is not claimed.
- Regression testing covers 19,475 passing test events across 16 distinct
  packages, including ND, maintenance, direct-device upgrades, SWIM and shared
  foundations. The broad invocation exited 1 solely because two package paths
  were incorrect; all 14 valid packages passed. Both provider packages passed
  separately at corrected paths. The final focused/API gates also include the
  later provenance wording and selected-omission test addition.
- The example CLI emits a full three-site source with one selected floor, one AP
  and one image, and keeps the complete asset snapshot.

Machine-readable results, fingerprints and gate log hashes are in
[the evidence record](site-managed-selection.json). The final source includes a
subsequent provenance wording correction and an added genuine-omission test;
the native mutation logic was unchanged after successful qualification.

## Diagnosed failures and limits

Two initial native attempts exposed a real bookkeeping bug: excluding a floor
retagged its image receipt as a declaration omission despite zero native writes.
Both generated fixtures were cleaned and baselines restored. The fix resolves the
receipt's exact current floor and checks selection before omission bookkeeping.
The passing native run retains a whole-journal equality assertion. Genuine
selected omissions are separately tested rather than weakening that assertion.

An initial race/API link failed from disk exhaustion; only old rebuildable Go
cache entries were removed. The first broad command also named two nonexistent
package paths; corrected provider tests pass. These setup failures are not code
regressions and are retained in the private historical logs.

Multi-file composition, installed selection, native bulk/physical flows, complete
schema/catalogue coverage, remote image byte readback and destructive AP/image
lifecycle remain pending. This qualification adds bounded execution support;
it does not establish complete parity for any untested resource behavior.
