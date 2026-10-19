# PnP final native preflight refusal recovery

The final PnP registration absence check now uses the same one-use configuration
guard and private refusal proof as the outer whole-manifest preflight. The native
primitive no longer maintains a separate atomic sender flag. Its existing UUID
receipt bridge forwards only its own guard-bound proof; the outer guard composes
that callback through the existing mechanism. No extra native reads, ownership
store, API field, physical grant or alternative writer was added.

This closes the inner-check limitation from the preceding
[outer preflight qualification](pnp-no-dispatch.md). Read failure or a newly
observed PnP/network-inventory identity in the last native absence check can now
retire the exact unchanged Submitting operation through the shared CAS path and
replan. The complete source still validates before planning. Native absence,
canonical stack member exclusion, private proof identity and existing
source/owner/controller/journal checks remain required.

Entering the POST closure ends the proof's scope. Conflicts, authentication
errors, invalid/lost/async acknowledgements and other post-entry outcomes do not
produce a refusal proof, even if an error resembles a preflight failure. Foreign
plans and already-claimed proofs are rejected. A changed owner or journal during
the final native read prevents retirement. Legacy Unknown cannot be cleared by
serial matching, reconstructed proof or a new plan.

| Gate | Result / scope |
| --- | --- |
| Race regression | 33 families / 616 subcases; 22.600s; PnP, proof/receipt and wireless regression |
| Real private Kubernetes API | Twelve outer/inner public cases; 14.041s; native service simulated |
| Native public registration | 11.554s; real inventories/registration/cleanup; Kubernetes persistence simulated |
| ND | 4.701s |
| Software-upgrade provider | 14.391s |
| Controller GNOI/SWIM | 6.414s |
| Production build | Passed; Darwin binary, not installed |

The added public/API cases deliberately allow both outer cohort observations
before failing the third active PnP read in the native primitive. They cover
native PnP conflict, final network-inventory read failure, newly managed device
appearance and concurrent owner/journal changes. Existing source-change cases
retain stale-publication suppression. A separate two-plan test rejects foreign
and already-claimed primitive proofs, refuses consumed-plan reuse and confirms
that a post-entry HTTP 409 cannot acquire no-dispatch authority. Existing
lost/async registration cases remain fenced without replay.

Native qualification allowed both complete outer cohort observations before a
local injected transport failure at the inner preflight. The original operation
retired with zero POSTs and retained inventory ownership. A new operation then
created one generated nonexistent-serial, unclaimed record. Serialized recovery,
owned no-op and exact UUID cleanup passed; repeated PnP/network reads restored
the initial baselines. The removed UUID was
`6ad413d80bc0186771caf0ee`. Missing owned records were not recreated or adopted.
No claim, reset or device action was submitted.

This qualifies process-driven public reconciliation with a real native service,
not installed actors. The isolated installed worker still runs the prior
credential-recovery image. Physical claiming, native workflow and preview
completion, mixed-state composition, public prune/delete and full pinned-model
parity remain pending. Historic evidence and source hashes are retained under
their original qualification boundaries.

See [source/operation/build record](pnp-primitive-no-dispatch-validation.json),
[regression](pnp-primitive-no-dispatch-regression.log),
[API](pnp-primitive-no-dispatch-api.log),
[native](pnp-primitive-no-dispatch-native.log),
[ND](pnp-primitive-no-dispatch-nd.log),
[provider upgrades](pnp-primitive-no-dispatch-provider-upgrades.log),
[controller upgrades](pnp-primitive-no-dispatch-controller-upgrades.log) and
[build](pnp-primitive-no-dispatch-build.log).
