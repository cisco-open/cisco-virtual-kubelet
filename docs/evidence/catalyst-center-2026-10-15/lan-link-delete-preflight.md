# LAN delete ownership and private native planning

## Implemented

The private DELETE_LINK planner now derives eligibility candidates from an actual
verified CVK ADD application receipt. Native presence alone is not an owner.
It requires the exact source declaration, retained section/config ownership,
current controller/source identities, canonical endpoints and managed footprint,
matching configured result and immutable dependencies, idle LAN service and
complete retirement of the predecessor's reservations. Released/settled rows
which still exist continue to fence the new destructive plan.

The optional pool is resolved from complete current IPv4 LAN pool inventory and
the matching verified ownership commitment when omitted. A supplied pool must
match that ownership. Resolution does not change the native payload: omission of
`ip_pool_name` remains omission of `ipPoolName`. The API documents that field as
required for ADD, not DELETE; see
[Cisco LAN Automation Device Update](https://developer.cisco.com/docs/catalyst-center/2-3-7-4/lan-automation-device-update/).
The flow applies to LAN-automated links as described in Cisco's
[delete-link workflow](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/tech_notes/cisco-catalyst-center-sda-lan-automation-deployment-guide/lan-automation-in-catalyst-center-release-235-and-later/delete-a-link-between-interfaces.html).

Configured ownership is separate from ADD liveness. Reachable devices and the
same verified interface configuration can qualify a private cleanup plan when
the link is down or LLDP adjacency is absent. Current positive interface/IP/mask,
pool/discovery and managed identities remain mandatory; a competing native
neighbor or changed configuration cannot become owned absence. ADD completion
and ADD owned InSync still require their existing positive UP result checks.

The ADD receipt hash format is preserved. Its successful-result format commits
mandatory UP states. A private configuration-ownership projection reconstructs
those original fixed success fields solely to compare configuration commitments;
it does not assert that a currently observed DELETE target is UP and never
modifies the observed snapshot or supplies a fake completion proof.

## Shared structure

ADD and private DELETE now use one frozen, one-use native sender and the existing
transport/session client. Native/source validation runs before the caller's guard;
authority is checked last before invoking the mutation. Missing runtime/authority,
changed source or port identity, and lost acknowledgement cannot replay the same
write instance. This is not cross-reconcile task recovery or a new dispatch permit.

Existing action applications now have a strict chronological effect reader and
publication guard. Duplicate keys/operation IDs, timestamp ties, malformed/legacy
records, foreign ownership and future generations hold. Synthetic format fixtures
cover ADD → DELETE → ADD ordering and monotonic publication; they are not actual
DELETE receipts or proof of successful native deletion. The current ADD producer
and ownership reader use this history check; consumption of a completed DELETE
effect remains disabled until the deletion lifecycle is implemented.

No CRD, controller transport, lease or status store was added. The existing
CC-specific role gains only namespaced `list` on configuration reservations,
matching the shared predecessor-retirement check already used by receipt
replacement. The rendered chart and compiled CC namespace-audit contract agree.
ND/base roles and device/status/lease write permissions are unchanged.

## Validation

The complete LAN race suite passed in **58.438s**. Private native HTTPS/fake
Kubernetes fixtures cover receipt-derived ownership, retired versus retained
predecessors, omitted/wrong pools, exact source binding, exhausted pool usage,
down links, absent neighbors, unreachable devices, foreign/unowned state,
changed configuration and managed identity, guarded LINK_DELETE payloads,
denied authority, changed ports, missing runtime, one-use submission and lost
acknowledgement. Ownership in these tests is derived through the guarded ADD
receipt/settlement writer; explicit fake device proof and manager actors are not
presented as installed qualification.

The complete affected race suite passed: Catalyst Center **347.626s**,
controller/manager **33.624s**, plus NetAsCode parsing, shared configuration,
device-worker configuration reservations, Nexus Dashboard and managed protocol
(cached passes). Runtime actor/descriptor and worker RBAC checks passed in
**3.731s**. See [full regressions](lan-link-delete-regression.log),
[runtime checks](lan-link-delete-runtime.log) and
[focused suite](lan-link-delete-focused.log). Strict documentation and whitespace
checks also passed.

Fresh read-only lab prerequisites passed in **9.140s**: two stable adjacencies,
maximum five sessions, zero active sessions and no recorded discovery history.
See [live prerequisites](lan-link-delete-live.log). The existing CVK Kubernetes API
still returns EOF; see [cluster probe](lan-link-delete-cluster.log). Neither probe
qualifies DELETE execution or deployed actors.

## Still pending

At this preflight milestone, public DELETE and typed journal admission were
disabled. The subsequent [journal/recovery milestone](lan-link-delete-journal.md)
adds private typed admission while keeping public DELETE disabled. Malformed or
pruned group identities remain reserved against single-device fallback.

At this milestone the remaining work included all-member prepare/dispatch and
acknowledged recovery, now addressed in the private journal milestone. Independent
deconfiguration proof, atomic delete receipt and settlement, deleted-state
idempotency/re-add and mixed action composition remain pending.
Installed/hardware qualification, legacy migration/adoption, broader model and
request-budget qualification also remain pending. No native deletion was submitted
to the Catalyst Center lab by this work.
