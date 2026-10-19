# Manager readiness and one-shot metadata worker claim

The existing metadata handoff now retains optional immutable readyAt evidence.
The manager publishes it separately after the complete original Lease UID cohort
and repeated source/owner/global-scope/actor/protection checks. Partial reference
sets cannot become ready, and completing references and readiness in the same
status update is denied. Readiness is copied independently and preserved during
refusal/release. Earlier acquisition evidence predates this final readiness CAS.

The acquirer recovers lost/conflicted/pruned readiness responses from a fresh
snapshot, preserving all original fence UIDs. A completed ready cohort remains a
no-op. Runtime nil still disables these writes; the composition root has no
compatibility callback and no installed deployment was changed.

ClaimMetadataOnce provides the shared worker protocol primitive for the existing
operation slot. It requires original ready custody, exact source/owner and worker
identity, all retained original fence incarnations, current compiled metadata/
bootstrap protection and a non-nil worker authorization callback. The callback
must verify the actual Pod-bound caller and deployed runtime contract. Product
eligibility must be supplied by the adapter's journal-start path before calling.
The primitive writes the original Prepared operation to Submitting with one
resourceVersion CAS and verifies independent readback. Only that successful
process receives a MetadataDispatchPermit. A conflict, lost response, pruned
readback or changed post-write source produces no permit and retains custody.
Persisted Submitting cannot reconstruct or replay one.

Permit copies share one consumption bit. Check consumes the opportunity even
when time, context or current authority is invalid. It rechecks source, original
operation/handoff, actor, retained fences and the same policy/namespace epochs.
Unrelated status-condition updates are tolerated after the claim, while Spec and
configuration changes are refused. The manager and worker share the original
pure metadata binding implementation; the worker performs no global device or
Node list. It still requires the role-appropriate namespace/Lease observations
and installed authorization before activation.

## Qualification and limits

Controlled cases cover missing/denied authorization, wrong Pod, absent/future
readiness, partial/replaced fences, source drift, lost/conflicted/pruned status,
post-write drift, copied and concurrent permit consumption, failed/cancelled
checks and unrelated condition updates. Acquisition cases include lost/conflicted/
pruned readiness publication in addition to earlier partial-acquisition tests.

The real isolated Kubernetes API enforces manager-only readiness and exact
Pod-bound worker progression. It denies a replacement caller, accepts the original
worker's journal CAS, rejects reconstruction from Submitting, and spends copied
capability state once. Original server-assigned Lease UIDs remain retained. Manager
acquisition, lost Create/reference-status response recovery, bootstrap retention
and historical actor checks run in the same final gate. The source-revision
negative case independently checks the binding after the staged claim, so it
cannot pass merely because scope observation requires Prepared.

Test clients have broad RBAC to isolate actual admission. Worker readiness,
background compiler status and remaining runtime compatibility are explicitly
staged. Product eligibility, actual running-worker authorization, native PnP
submission and installed/lab execution are not qualified by these tests.
[Results and exact source fingerprints](pnp-metadata-worker-claim.json) record the
gates, build, generated schema and documentation checks. Final API qualification
passes 79 test/package events; seven shared API/manager/ND/upgrade packages pass
3382, and PnP/SWIM compatibility passes 1193. Main build and chart rendering pass.

## Remaining execution work

The typed original Prepared PnP removal plan must be reconstructed without
creating a new operation or bypassing complete native cohort preflight. The
adapter's existing journal start must consume this shared claim and reobserve
its original authority immediately around guarded native submission. Existing
adapter handoff start/prepare/finish barriers remain in place until that path is
implemented. No public new pruning or native dispatch is enabled by this change.
Original acknowledged/independently verified absence or certified non-dispatch
must then support exact-UID settlement, uncertain/restart recovery, manager
release acknowledgement and handoff retirement. The full installed manager/
worker/enrollment/settlement contract remains required before enabling the
composition-root callback. Complete consumer protection, deletion/missing-source
finalization and wider pinned 93-resource execution/hardware gaps remain open.
