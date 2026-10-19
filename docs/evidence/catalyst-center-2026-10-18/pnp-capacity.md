# PnP registration ownership capacity admission

Public registration now budgets the **complete remaining declared cohort** before
its first native POST and repeats that check inside the existing whole-cohort
source/owner/journal dispatch bracket. The budget includes all existing action
receipts across sections. Prospective PnP receipts reserve the full 128-byte
native ID bound, actual configuration/source metadata and verification envelopes.
The limits match other resource-specific metadata admission checks: 256 receipt
rows and 256 KiB of serialized receipt data.

This is admission under the existing controller-wide serialized journal, not a
new reservation store or schema. Existing receipts are not evicted, adopted,
rewritten or transferred. Fully owned selection requires no new receipt and can
reach no-op at the row limit. Splitting manifests does not increase this shared
controller budget.

Owned registration publication separately checks the actual prospective receipt
before the existing atomic Verified/receipt CAS. Concurrent state changes remain
fenced by the common source/journal/resource-version checks. If capacity becomes
insufficient after native acknowledgement, the original Verifying operation and
created UUID remain retained; no native mutation is replayed or inferred rolled
back. `PnPRegistrationCapacityUnavailable` reports this separately from an
uncertain submission. Late pre-dispatch capacity changes use the existing private
no-dispatch proof when eligible, then a fresh plan checks current capacity again.

| Gate | Result / scope |
| --- | --- |
| Race regression | 34 families / 626 subcases; 36.437s; PnP/proof/receipt/wireless paths |
| Real private Kubernetes API | Eight capacity cases; 56.036s; native service simulated |
| Native lab | 14.705s; three blocked capacity scenarios plus ordinary public registration/cleanup; Kubernetes persistence simulated |
| ND | 4.847s |
| Software-upgrade provider | 14.387s |
| Controller GNOI/SWIM | 6.452s |
| Production build | Passed; Darwin binary, not installed |

Controlled/API cases cover full rows, insufficient room for the complete cohort,
byte exhaustion, exact-limit publication/owned no-op, late row/byte changes and
capacity loss before publication. At the boundary, 255 historical receipts plus
one new owned registration reach 256 without changing history. Publication
failure preserves the acknowledged UUID and original operation. Large fixtures
use unique effect entries required by the CRD's set semantics.

The old 1,000-declaration read-budget test treated syntax acceptance as execution
admission. It now separately proves that a bounded 256-declaration empty cohort
is admissible and that 1,000 new declarations are refused before mutation. Both
still use one pair of complete inventory reads rather than per-device fetches.
This exposes a **remaining scale gap**: the full model's larger declarations need
an ownership-storage design beyond the current shared ledger. No truncated
selection, receipt eviction or unqualified sharding is introduced.

Native capacity tests injected only synthetic occupancy into their own simulated
Kubernetes stores. Row, byte and two-device cohort exhaustion preserved every
fixture receipt and made zero POST/DELETE calls while repeated real PnP/network
reads retained the native baselines. No installed ledger was read or trimmed.
A separate ordinary public cycle created one generated nonexistent-serial,
unclaimed record, persisted its ownership, reached no-op and removed exact UUID
`6ad416770bc0186771caf129` through supervised cleanup. Repeated native reads
restored the original baselines; no claim, reset or device action was submitted.

The raw native primitive has no Kubernetes ledger context; this admission guards
the public whole-cohort stage and owned publication. Installed actor qualification,
physical claiming/workflow/preview completion, mixed-state composition, public
prune/delete and full pinned-model parity remain pending. The isolated installed
worker still runs its prior credential-recovery image.

See [source/budget/checkpoint record](pnp-capacity-validation.json),
[regression](pnp-capacity-regression.log), [API](pnp-capacity-api.log),
[native](pnp-capacity-native.log), [ND](pnp-capacity-nd.log),
[provider upgrades](pnp-capacity-provider-upgrades.log),
[controller upgrades](pnp-capacity-controller-upgrades.log),
[build](pnp-capacity-build.log) and
[pre-dispatch recovery](pnp-primitive-no-dispatch.md).
