# Area, building and floor manifest lifecycle

This qualification extends the existing public private-site manifest route to a
generated area → building → floor hierarchy. It reuses the same provider,
transport, ownership, consumer guards, original-task recovery and atomic receipt
publication. No production provider logic, CRD, RBAC or upgrade flow changes were
needed for the three-kind extension.

The new native qualifier independently restricts creation to exact generated
names, desired geometry and authenticated current parent creator IDs. Deletes
require explicit one-use allowances for each verified kind/UUID. Unrelated
mutations and repeated sends are refused. The allowlist has a dedicated test for
foreign identities, wrong methods/endpoints, extra fields and exhausted permits.
An interruption leaves the original source, journal and generated test-only key
in a private checkpoint; there is no speculative cleanup or mutation retry.

The source model follows pinned NetAsCode 0.5.0 site fields. The example is
`examples/configs/catalyst-center/sites-private-hierarchy-lifecycle.yaml`.
It uses a ConfigMap source, the normal private-key projection contract,
`prunePolicy: Delete`, `deletionPolicy: Retain` and bulk mode disabled. The
example was parsed and its three kinds and parent paths checked; native testing
uses unique generated names rather than applying the example as a second owner.

## Test boundaries

The [validation record](site-hierarchy-lifecycle-validation.json) pins exact
source hashes, task IDs and completed logs. Final race-enabled gates passed:

- Manifest fixtures: three families, 25 subcases, 18.495 seconds.
- Real API-server fixtures: two families, eight named subcases plus the complete
  three-kind lifecycle, 36.157 seconds.
- Native mutation allowlist: one family, 2.359 seconds.
- Native public hierarchy lifecycle: one family, 134.507 seconds.

The native cycle created three empty sites, pruned all three, recreated all three
with new UUIDs, then pruned them again. All twelve distinct tasks were recovered
after acknowledgement was retained as Unknown. It completed six Create POSTs and
six Deletes, retained three archived predecessor cohorts (six archived rows),
twelve total receipt rows and one owner, and finished with no active operation.
The complete typed inventory matched its initial baseline. Replacement building
and floor creators both bound their new parent's creator receipt. The legacy
log label “create task” covers all actions; actual action sequence and native
method counts are checked independently.

Public reconciliation fixtures exercise initial creation, leaf-first removal,
parent-first recreation with replacement parent UUIDs, and final leaf-first
cleanup. Both inline and ConfigMap sources retain the same configuration owner
and complete predecessor history. Separate real API-server fixtures cover the
three-kind cycle and existing area refusal cases. Every acknowledged task in
these lifecycle fixtures is made Unknown before recovery.

Initial fixture runs failed because floor planned-AP inventory was missing.
The fixture now explicitly returns a complete empty inventory; the production
consumer guard remains unchanged. Initial and corrected gate logs are retained
separately so the correction does not overwrite evidence of the refusal.

Native component execution uses simulated Kubernetes persistence. Real API-server
tests are separate and do not prove installed worker/restart qualification.
No device is adopted, claimed, upgraded or reconfigured. Empty-site lifecycle
does not prove image/AP/policy-effect cleanup, populated LAN workflow handling,
configuration deletion or full NetAsCode parity. Those remain pending.


Subsequent [installed lifecycle qualification](installed-site-lifecycle.md) uses
real worker execution and Kubernetes persistence. Production building exposed a
test-only creator lookup dependency; that follow-up reuses the existing production
matcher and preserves an explicit new source boundary. The component hashes and
gates above remain historical. Installed empty-site lifecycle is now qualified;
retained-effect cleanup and complete model parity remain pending.
