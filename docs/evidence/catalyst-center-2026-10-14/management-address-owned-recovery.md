# Owned native-stage management-address recovery

A private adapter recovery observer now combines the existing configuration
snapshot and ownership checks with retained old endpoint/device/Node/worker
evidence, the shared single-device claim/live-lease observer, exact current
journal and task receipt, and the existing isolated controller-worker binding.
The requested reference must occur exactly once in the current parsed inventory
source; the config must be Apply/global and manage inventory.

Observation requires the current owned Verifying operation and a configured
single-device reservation runtime. It observes the exact native task tree, then
uses the native UUID/serial/address verifier with separately supplied endpoint
proof. Source/owner/controller/worker/lease evidence is repeated after proof.
Success is a **native-stage observation only**: it does not submit, claim,
advance/clear the journal, patch a device or release a lease. Final declaration
and worker rollout and the complete verified receipt remain a separate stage.

Fixtures use the real common worker and lease checks with simulated Kubernetes
objects. Complete native-stage observation succeeds; running/failed tasks,
native state pending, denied endpoint proof, missing owner, changed source,
revoked lease, undeclared source reference and ownership changed during proof
are refused. Every case confirms the retained journal remains unchanged and
recovery issues only read-only native requests. The combined race run passed:
Catalyst Center 3.837s and shared configuration 3.943s; see
[retained output](management-address-owned-recovery.log).

The current observer is not wired as public compiler execution or final
reconciliation completion. Real endpoint proof, initial dispatch permits,
automatic CVK address/worker alignment, verified receipt/settlement publication,
installed actors and an accepted isolated live transition remain pending.
No lab mutation was performed and no full resource/section parity is claimed.
