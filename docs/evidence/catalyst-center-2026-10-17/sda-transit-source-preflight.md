# SDA whole-source prerequisite preflight

Before preparing the first SDA transit action, CVK now checks every SDA transit
in the declared source. A later unprovisioned, unassigned, unmanaged, conflicting
or shared-physical control plane prevents the first journal entry. An existing
matching peer definition is accepted as a dependency, without acquiring ownership.
This extends the staged execution path; new public SDA activation remains gated.

## Execution and recovery boundaries

The source preflight reuses the existing complete-footprint managed observer.
Within each observation it shares native device, site, transit and per-site
assignment inventories across declarations. Provisioned-device reads remain per
selected physical device. Each declaration receives a separate peer map, so
excluding its own result cannot alter another declaration's dependencies. Fresh
observations are still taken around managed identity checks; caches never cross
an observation, dispatch or recovery boundary. Source order does not change the
combined observation digest.

All declared CPs form the read dependency population. Only the selected transit's
CPs enter its persisted physical action group and shared submission permit.
Whole-source validation repeats in Prepared and within the Submitting guard;
a late prerequisite failure prevents the native POST and retains the journal.
The current scope is bounded to 1,000 declared CP references across the source,
matching the existing managed observer's population bound. Larger-source batching
is still needed; this is not an unlimited-scale implementation.

After native acknowledgement, recovery continues to require the original exact
resource, selected physical group, source/owner identity, native task and readback
proofs. A missing prerequisite on an unrelated pending declaration no longer
prevents finding that exact retained action, including when the unrelated entry
appears earlier in the source. Its own failed dependencies still refuse recovery.
No receipt, semantic-convergence proof or reservation release is inferred.

## Verification

- [SDA race regression](sda-transit-source-preflight-regression.log): 139 passing
  subcases plus standalone checks, 23.392 seconds. Added coverage includes later
  declaration failures, existing matching peers, selected-only groups, late
  Prepared/Submitting refusal, target-specific acknowledged recovery in both
  source orders, source bounds and shared observation read counts.
- [Five real private API cases](sda-transit-source-preflight-api.log): 32.549
  seconds. Valid two-transit source preparation persists only the selected
  one-device group. A later invalid declaration leaves no configuration journal
  or owner. Existing source/alias drift and field-pruning refusal also pass.
  Product responses, workers and grants remain simulated.
- [Live read-only qualification](sda-transit-source-preflight-native.log): all
  three synthetic aliases resolve to the exact existing lab device identities.
  All three lack native provisioned records. A combined three-transit source
  also refuses readiness. Zero product writes; no live CLI or SDA creation.
- [Adjacent controller regressions](sda-transit-source-preflight-adjacent.log)
  and [shared foundations](sda-transit-source-preflight-foundations.log) cover
  wired/transit/group behavior, ND, device operations, diagnostics, lifecycle,
  upgrade and GNOI. Unchanged foundation packages may use cached results.
- [Source and pinned upstream fingerprints](sda-transit-source-preflight-sources.json)
  and [strict documentation build](sda-transit-source-preflight-docs.log).

## Remaining work

Independent semantic transit convergence, final durable receipts and reservation
settlement remain open. Provisioning orchestration, automatic Prepared dispatch,
full mixed-model composition and broader inventory fields are not activated.
Updates and connected consumers, creator/adoption/destruction ownership,
installed actors and accepted live SDA execution still require implementation
and qualification. The full pinned NetAsCode model goal remains incomplete.
