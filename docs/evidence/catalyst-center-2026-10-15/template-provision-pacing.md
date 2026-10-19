# Scoped provisioning-to-template settling

## Upstream contract and implementation

The pinned NetAsCode 0.5.0 module, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`, declares
`time_sleep.provision_device_wait` in `cc_device_provision.tf`: ten seconds after
provisioning in scoped-device mode. Its regular, composite and unmanaged
deployments depend on that wait in `cc_templates.tf`.

CVK's currently supported scoped wired inventory compiler now connects that
interval before regular and composite deployments. It includes every declared
PROVISION/REPROVISION target, including devices without templates. The compiler
keeps all native device/serial/site bindings stable during planning and dispatch.
ASSIGN-only targets do not become artificial provisioning dependencies.

Current-revision verified wired provisioning action receipts supply `VerifiedAt`.
For already-provisioned devices without a current receipt, the exact deployment's
persisted `PreparedAt` supplies an observation floor. Legacy missing timestamps
remain pending. Duplicate/foreign receipts, replaced serial/site identities,
changed source/controller revision, future or inconsistent times and mismatched
journals cannot satisfy the interval. A one-second margin accounts for timestamp
serialization to whole seconds.

The existing adapter-local `preparationWait` mechanism now also accepts physical
inventory template operations. It populates the existing optional `PreparedAt`
field; it adds no schema, receipt collection, transport or reservation protocol.
Unsupported physical operations still cannot request this mechanism. A timestamp
does not authorize dispatch: the managed reservation/claim and one-shot submission
path remain required.

Planning without an outstanding operation may produce a candidate for preparation.
Prepared resume and the final submission callback both recheck settling. Accepted,
Unknown and Verifying recovery retain their existing paths; pacing does not replay
an accepted deployment or delay completion/settlement evidence.

## Verification scope

The public manifest lifecycle fixtures cover regular/composite/private ON_CHANGE
deployments, rollback, multiple devices/templates, lost receipts, failed tasks,
source replacement/deletion and no-repeat convergence. Two additional public
cycles use actual elapsed time across restarted reconcilers: a new provisioning
receipt before a regular template and already-provisioned devices before a
composite template. They assert retained Prepared identity and zero writes during
the interval, then complete deployment/readback/InSync through simulated actors.
Other lifecycle variants explicitly simulate elapsed time in persisted evidence.
These are HTTP/Kubernetes fixtures, not installed actor/Lease qualification.

Focused timestamp tests cover recent/boundary/settled verification, missing and
legacy timestamps, existing-device fallback, a second dependency, owner/source
changes and stale identities. A physical-admission test proves preparation alone
cannot call the native submission callback.

The initial real-time fallback test exposed that physical operations did not yet
receive `PreparedAt`; the adapter-local mechanism was extended and both elapsed
cycles then passed. The obsolete broad run was explicitly stopped after that code
change; it is not cited as final verification. Final results are in companion logs.

## Native lab prerequisites and remaining work

Read-only authenticated native inventory and `provisionDevices` queries found all
three lab switches reachable, with no provisioning records:

| Management address | Hostname | Serial | Observed version |
| --- | --- | --- | --- |
| 198.51.100.100 | C9K-1 | FOC2520L6E8 | 17.18.3 |
| 198.51.100.101 | C9K-2 | FOC2520L6H1 | 26.02.1 |
| 198.51.100.103 | C9K-4.dmz.cisco.com | FOC2416U0MV | 17.18.3 |

The existing live bulk planner and deliberate one-use dispatch-refusal test also
passed for all three native identities, with unchanged provisioning state
(5.645s overall). No provisioning or template mutation was submitted during this
qualification.
The offline CVK cluster has a verified private backup; isolated runtime recovery
and resumed device operations are outside that backup approval. A full native
cycle still requires restored/admitted actors, settled site assignments, actual
provisioning, applicable committed templates and verified deployment receipts.
Full wireless/bulk composition and complete NetAsCode resource parity remain open.

## Results on the corrected revision

- Public manifest lifecycle, real elapsed waits, durable pacing and journal race
  tests: pass (56.923s).
- Physical preparation cannot bypass dispatch admission: pass (2.555s).
- Direct software-upgrade tests: pass (cached); maintenance: pass (6.695s);
  mutation guard: pass (2.856s).
- Read-only native planning and deliberate dispatch refusal: pass (5.645s).

Companion logs distinguish local simulated-actor cycles from native read-only
qualification. No native provisioning/deployment or installed Lease qualification
is claimed.

Final manager/connector/worker RBAC checks pass (4.033s).

Final full affected race suite: pass. Catalyst Center completed in 558.681s;
NaC model, configuration journal/reservation, ND, controller and managed-protocol
packages passed (cached). Strict documentation build passed (4.59s before this
results append); the final post-append build is recorded in the companion log.
Whitespace validation passed. The separate live-tag profile tests qualify
FlexConnect/Fabric definitions and are documented in
[mode evidence](wireless-profile-modes-native.md).
