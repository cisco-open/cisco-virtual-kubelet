# Exact PnP preparation binding for manager scope observation

The optional adapter registry now exposes pure metadata-scope selection and
physical-identity projection callbacks. They carry no clients, factories or
product credentials. Registry validation rejects incomplete callback pairs and
lookup/registration clone their callback structs. Model-source formatting is
redacted because non-Secret configuration may still contain sensitive intent.
Adapters without the callback preserve their existing path; ND adds no callback.

Catalyst Center selects retained PnP removal protocol families and accepts only a
strict Prepared v2 metadata Delete. Its projection checks current config/owner,
controller/source revision, complete pinned PnP prune intent, every canonical
creator baseline, tombstones, receipt capacity and deterministic target order.
The selected target and resource digest must match the first pending creator.
It returns all remaining canonical serials in stable order; it never reconstructs
old raw serial casing or sets the physical TargetSerial field. Inline source bytes
must equal the current declaration; ConfigMap identity must match the preparation.

The manager's existing reservation-preparation route now selects this optional
metadata path when there is no physical target serial. It reads the current
namespaced config and inline/ConfigMap model, invokes the pure projection without
constructing an adapter, then calls the shared uncached metadata scope observer.
That observer repeats source/owner/controller checks and global identity,
retained-reservation and bootstrap-fence observations. Missing installation or
lease namespace, stale source, invalid projection and retained conflicts refuse
the prerequisite. Success keeps the operation Prepared and requeued; it publishes
no attestation, reservation, Lease, worker grant or native submission permit.

Workers do have limited global observation roles, but those roles do not grant
the global Node/device/controller census required here. No worker RBAC expansion
is introduced. Physical reservation and SWIM/direct-device contracts are unchanged.

## Qualification

Thirteen provider scenarios run with both controlled and actual API persistence
(29 test/package events). They cover exact complete projection, substituted config
UID, later UUID, creator digest, absent/legacy baseline, ownership, source, inline
bytes, submitted/legacy/physical operations, retained policy and lost creator.
Inputs and persisted preparation remain unchanged.

Manager cases cover current inline/ConfigMap models, replaced ConfigMap revision,
missing keys, missing installation/namespace, unsupported projection, Node,
reservation and fence conflicts, submitted work, wrong owner and unselected/ND
paths. The manager never instantiates the product adapter. Registry isolation and
incomplete-contract tests pass. These and existing foundation/ND/upgrade tests
pass 3143 test/package events across six packages. Selected PnP/journal/upgrade
regressions pass 1046 events. The production binary and formatting/source checks
pass; [results and hashes](pnp-manager-binding.json) record the gates.

## Remaining production handoff

This increment wires manager read-only observation only. Installed actor/version
binding and protected serialization from observation to worker dispatch remain
incomplete. It does not authorize new public PnP Deletes, equate a scope read with
a grant or qualify a new native mutation flow. Native consumer protection,
missing-source/CR deletion context, finalization and installed full lifecycle
qualification remain pending. The full pinned 93-resource model is incomplete.
