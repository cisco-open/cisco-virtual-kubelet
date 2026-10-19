# Canonical PnP removal identity bound to its creator

A private baseline-capture stage now retains cleanup identity while the exact
original manifest and verified registration are still available. This closes the
serial-case reconstruction gap: registration hashes the supplied serial, while
native inventory normalizes it. A later cleanup must not guess that original
payload after source removal.

The versioned extension of `pnp-record:<uuid>` stores the canonical serial using
separator-safe encoding, a canonical registration digest and the safe native
snapshot digest. It stays inside the existing 512-byte result field. The original
Create operation, source binding, UUID and verification time remain unchanged;
no row, CRD, credential or permission is added. Legacy records continue to attest
registration, but do not acquire this cleanup baseline automatically.

Capture requires the exact supported source, current section ownership, a settled
original creator, uncontacted native identity and repeated authority/native
checks through the existing removal planner. It observes that planner without
calling its Delete function. The existing controller status CAS enriches only the
selected receipt. Lost acknowledgement can be recovered by reading the persisted
extension; status conflicts require a fresh plan. An existing changed baseline is
refused rather than overwritten. Shared row/byte capacity is rechecked and no
receipt is evicted.

## Qualification

Controlled and isolated Kubernetes API tests pass source/ConfigMap fences,
foreign/missing ownership, active-operation refusal, native drift, immutable
baseline preservation, full-row-count enrichment, byte-capacity refusal,
status conflicts and lost acknowledgements. Encoding tests cover maximum-length
serial/UUID values, separator-containing serials and invalid protocol/digest/
encoding variants. Comparisons use the authoritative API-stored original receipt,
including the server's time serialization.

The real-API/controlled matrix passes (38 test/package events), as do
526 selected PnP/template-reference/SWIM/reload regression events, five
foundation packages and the main binary build. The native generated-record cycle
passes baseline capture, JSON-persisted restart, unchanged original operation/time,
owned registration no-op without another POST, and exact guarded cleanup with
restored PnP/network baselines. It used one POST and one DELETE and made no
physical claim/reset/device action. Kubernetes persistence in that native cycle
is simulated; the actual API-server cases are separate. See
[results and source hashes](pnp-removal-baseline.json).

## Scope and next execution gates

This is a source-bound ownership prerequisite, not manifest-driven deletion.
Public registration still advertises only its existing bounded stage. Public
cleanup needs durable Delete preparation, acknowledgement and absence receipts,
recovery without replay, omitted-source routing, complete consumer/private-content
protection, explicit cleanup policy and manager finalization. The native digest
binds the documented safe projection; it is not proof about discarded private
configuration fields. Installed actors, stacks, contacted records, physical
claiming and complete pinned NetAsCode parity remain pending.

The related [private native removal](pnp-uncontacted-removal.md) remains separate
from durable public cleanup. No permission to adopt imported records or overwrite
missing identity evidence is inferred from this baseline.
