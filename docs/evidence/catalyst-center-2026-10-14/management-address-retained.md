# Retained management-address baseline and worker recovery

Recovery now reconstructs the pre-mutation address from the immutable
configuration reservation, rather than guessing it from controller inventory
after a successful address edit. The helper reuses the common operation-binding
digest and validates namespace, reservation incarnation, native UUID, canonical
physical serial, device/Node references, correlated task and submission evidence.
The recovered old/new addresses must reproduce the retained resource key.
Prepared, cancelled, taskless or mismatched operations return no baseline.

A separate read-only retained-binding helper rereads the reservation and uses
the existing managed device/Node resolver plus `ReadDeviceWorkerBinding` against
that admitted **old-endpoint view**. It compares the retained device/Node UIDs
and rereads the reservation spec/incarnation. This is explicitly historical
baseline evidence, not a rewritten current controller inventory projection.
Exact common address agreement and the worker generation/revision/readiness
checks remain unchanged. Cross-namespace aliases are still refused.

Fixture tests cover Accepted, Unknown-with-task and Verifying baseline recovery;
wrong old/new addresses, namespace, serial, digest or native UUID; missing
reservation UID/task evidence; cancellation and Prepared refusal. Live-object
fixtures additionally reject changed device address/generation, replaced or
unready workers, replaced nodes, aliases and reservation reincarnation. The
combined race regression, including prior managed/native and common identity
tests, passed in 3.309s. See
[retained output](management-address-retained-worker.log).

These helpers grant no lease or submission permission and do not prove current
source ownership, lease retention, endpoint reachability or task completion by
themselves. The public recovery bridge must still validate current owner/source,
controller worker, held reservation/lease and native task; obtain independent
old/new endpoint physical proof; then coordinate declaration/worker rollout and
safe settlement. The reservation's address, device generation and worker
revision are immutable and cannot be silently advanced in place.

No lab mutation occurred in this increment. Accepted live migration and
installed actor qualification remain pending; full parity is not claimed.
