# PnP registration pre-dispatch retirement and replanning

The public PnP-only registration stage now reuses the existing guard-bound,
one-use proof and journal CAS retirement when its final whole-manifest
source/owner/cohort check refuses **before calling the native registration
primitive**. The existing synchronous-created-UUID receipt path can carry that
proof. The inventory allowlist accepts only canonical `pnp-register:<sha256>`
Create writes with the synchronous-created writer and no physical/dependency or
credential identities. Claim resources and other inventory actions are excluded.

A proven refusal clears only the exact unchanged operation, preserves section
ownership and publishes `PnPRegistrationReplanRequired` when the original source
is still current. A changed source suppresses stale status through the existing
publication fence. The next reconcile builds a fresh complete plan. No native
result, serial match or HTTP error can manufacture the process-local proof.
Proofs cannot be reused after retirement or reconstructed after restart.

The native registration primitive retains its independent last absence check.
Failure inside that primitive or after entry to POST still lacks the outer proof
and remains Unknown. Lost/async/invalid acknowledgements, legacy Unknown records,
changed owner/controller/journal bindings, failed CAS and already recorded native
identities remain fenced. This change does not authorize replay or native adoption.

| Gate | Result / scope |
| --- | --- |
| Race regression | 32 families / 610 subcases; 21.815s; PnP, synchronous-created receipts, proof safety matrix and wireless replan regression |
| Real private Kubernetes API | Six public cases; 11.742s; native service simulated |
| Native public registration | 10.711s; real inventory/registration/cleanup, simulated Kubernetes persistence |
| ND | 4.541s |
| Software-upgrade provider | 14.532s |
| Controller GNOI/SWIM | 6.451s |
| Production build | Passed; Darwin binary, not installed |

Controlled/API cases include final read errors, foreign cohort appearance,
ConfigMap/source changes, changed owner and changed operation. The shared proof
matrix additionally covers cancellation, CAS conflict/exhaustion, foreign/spent
proofs, task/receipt contamination and ordinary/lost-ack errors. Existing PnP
lost/async tests continue to preserve Unknown and refuse replay. Initial fixture
failures were corrected by setting the endpoint before real API creation and
providing a UID in simulated ConfigMap persistence. Source-change tests explicitly
expect publication suppression rather than weakening the common fence.

Native qualification injected a local transport failure only when the original
Submitting operation reached its final cohort read. It verified zero registration
POSTs and retained ownership after retirement. A fresh plan then created exactly
one generated nonexistent-serial, unclaimed record with a different operation ID.
Serialized reconciliation, exact UUID ownership and no-op passed. Supervised
cleanup removed UUID `6ad4122d0bc0186771caf0c8` only after repeated unclaimed/
not-contacted, identity and unchanged network-baseline checks. Repeated native
reads restored the empty PnP and shared network baselines; removed owned records
were not adopted or recreated. No claim, reset or device endpoint was submitted.

Installed qualification of this new path, physical admission/claim/workflow
completion, preview sequencing, mixed-state composition and public prune/delete
remain pending. The isolated installed manager/worker still runs the preceding
credential-recovery image; this binary was not rolled out. Full pinned 93-resource
parity is not claimed.

See [source/operation/build record](pnp-no-dispatch-validation.json),
[regression](pnp-no-dispatch-regression.log), [API](pnp-no-dispatch-api.log),
[native](pnp-no-dispatch-native.log), [ND](pnp-no-dispatch-nd.log),
[provider upgrades](pnp-no-dispatch-provider-upgrades.log),
[controller upgrades](pnp-no-dispatch-controller-upgrades.log) and
[build](pnp-no-dispatch-build.log).
