# Public LAN ADD Apply lifecycle

## Implemented

A resolved NetAsCode 0.5.0, retained, global, add-only `lan_automation.links`
manifest now reaches a dedicated public Apply route. The route precedes generic
unsupported-section handling and reuses the existing CC group implementation:

1. Parse the complete source. Mixed deletion, discovery-device declarations,
   unknown fields, duplicate/shared ports and unresolved inputs cannot be dropped.
2. Observe every declared link's native/managed prerequisites. An unchanged owned
   link requires its verified application receipt plus current result equality;
   unowned addressed ports cannot be adopted.
3. Prepare the first unexecuted link in deterministic name order using the
   existing atomic journal. Preparation creates no reservations or device writes.
4. On the next reconciliation, match the exact Prepared operation against the
   complete source and original physical group. Revalidate before requesting the
   existing all-member claim/one-use permit. Missing grants, stale source,
   replaced identity, active sessions or changed prerequisites hold.
5. Dispatch one native PUT through the existing frozen LAN ADD writer and permit.
   Accepted work is observed; an unacknowledged Submitting outcome cannot replay.
6. Verify rooted task completion and native results, then consume both independent
   DeviceOperation worker proofs through the existing
   [device verification bridge](lan-link-device-proof.md).
7. Publish the existing atomic Verified receipt and shared member completion.
   Worker release and manager settlement must finish before journal retirement.
8. Recognise unchanged settled owned links without another mutation claim. Public
   `InSync` means the declared links retain independent completion receipts and
   match current native result observations. It is **not** a fresh IS-IS neighbor
   attestation on every reconciliation.

`lan-link-apply` is included in the CC descriptor so the manager/worker contract
digest reflects this execution capability. No new CRD, transport, authority
protocol or status store was introduced. ND and direct-device upgrade paths are
unchanged.

## Example and observation

The pinned example at `examples/configs/catalyst-center/lan-link-add.yaml`
uses documentation addresses and must be populated with actual recorded discovery
members, full interface names and a same-site IPv4 LAN reservation. It is an
add-only Apply example; Report, discovery execution and deletion are not enabled
by this example.

Useful observations during execution:

```sh
kubectl get networkcontrollerconfig catc-lan-links -n cvk-live -o yaml
kubectl get networkcontroller catc -n cvk-live -o yaml
kubectl get controllerconfigurationreservations -n cvk-live
kubectl get deviceoperations -n cvk-live
```

Configuration conditions distinguish preparation, all-member admission,
submission, task/device verification and owned convergence. The retained
NetworkController operation and original endpoint reservations remain the source
of recovery truth.

## Qualification

Native HTTPS/fake Kubernetes tests cover public preparation, missing runtime or
grants, changed configuration generation, mixed unsupported deletion, invalid
later links, unowned addressed interfaces, admitted dispatch, denied members,
active sessions and lost native acknowledgement. Repeated reconciliation cannot
replace the original operation or repeat a native request.

A composed public test starts from an unprepared manifest, exercises the actual
shared claim/dispatch code with explicit fake Deployment/worker/Lease actors,
enters Verifying, creates both read operations, consumes explicit fake protected
worker results, and reaches the atomic Verified receipt. It then reuses the
established settlement fixture to check denied actors, partial/lost completion
acknowledgements, incomplete release and all-member manager settlement. Two
subsequent public reconciliations reach owned InSync with one native update total
and no new mutation authorization.

The targeted public/proof composed suite passed in **14.757s**. The complete LAN
and descriptor race suite passed in **34.223s**. These are fixture qualifications,
not deployed actors or physical-device mutation evidence. See the
[composed cycle](lan-link-public-cycle.log) and
[complete focused](lan-link-public-focused.log) logs.

Runtime descriptor/admission and managed-worker role checks passed in **3.756s**;
see [runtime log](lan-link-public-runtime.log).

Fresh read-only Catalyst Center qualification passed in **5.285s**: both native
lab adjacencies were stable, with zero active LAN sessions and no session history.
See [live prerequisites](lan-link-public-live.log). The example YAML parsed with
the pinned model version and ADD_LINK shape. Strict documentation validation and
whitespace checks passed.

The complete affected race suite passed; see
[regression log](lan-link-public-regression.log). Coverage includes the full CC
package and NetAsCode parser, shared configuration protocol, Nexus Dashboard,
controller/manager logic and managed protocol. Installed actor qualification is
still separate from these package results.

## Remaining

The existing `kind-cisco-vk` API still returns EOF; see the
[current readiness probe](lan-link-public-cluster.log). Its previously captured
containerd recovery failure prevents installed qualification. Read-only CC
prerequisites must not be presented as a submitted native LAN operation; the lab
also has no recorded LAN discovery history for the required origin binding.
No factory reset, discovery session or device/link mutation was performed.

Live CLI profile and accepted LAN mutation, installed actor/Lease settlement,
Report, explicit DELETE_LINK ownership/recovery, discovery Start/Stop, adoption,
legacy receipt migration, changed-source/immutable-scope cancellation, large-model request-budget qualification,
cross-site/shared capacity and broader model composition remain pending. An eligible changed
pre-dispatch baseline now uses [cancellation and replanning](lan-link-replan.md)
instead of refreshing the original admitted scope. Unobservable or changed
source/immutable identity evidence remains fenced.
