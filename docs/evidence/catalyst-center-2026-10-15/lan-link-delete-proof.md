# LAN DELETE: independent device evidence and atomic completion

On `pr/johalley/catc-support`, private LAN DELETE now composes the existing
DeviceOperation workers, source/native/task convergence bracket, atomic group
receipt writer, and worker/manager settlement protocol. Public Apply still
refuses DELETE. This milestone does not establish full NetAsCode LAN parity,
accepted live native deletion, or installed worker qualification.

## Evidence contract

ADD and DELETE use one worker-proof lifecycle. Stable read identities bind the
controller/operation/group/task, rooted task completion, physical device worker
and result fingerprint. The existing manager supplies protected worker bindings.
The connector receives no device credentials and opens no direct device transport.
Creation/start/completion timing, observed generation, Ready/Succeeded status,
exact commands, ownership, worker identity, freshness, output limits, redaction,
truncation and artifacts retain the existing ADD validation contract. Unfinished
reads stay bounded, and terminal read cleanup uses the existing five-minute TTL.

DELETE requests only `show version` and `show running-config interface <exact>`.
The bounded IOS-XE parser requires exactly one matching chassis serial, one exact
interface block with an explicit `no ip address`, and its terminating `end`.
Remaining IP addressing or IS-IS commands, missing/duplicate blocks, unknown
commands/layouts, errors and trailing content hold verification. Shutdown and
physical LLDP state do not establish or invalidate deconfiguration by themselves.
An empty neighbor table is not used as proof. The parser supports a deliberately
bounded output profile; its match to actual post-DELETE lab output is unqualified.

Cisco's [Delete Link guide](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/tech_notes/cisco-catalyst-center-sda-lan-automation-deployment-guide/lan-automation-in-catalyst-center-release-235-and-later/delete-a-link-between-interfaces.html)
documents deleting links created during LAN automation or Add Link and requires
reachable managed devices. It does not specify the exact resulting running
configuration. The strict CLI profile above is CVK's verification design, not a
claim that Cisco documents that exact output.

## Receipt and settlement

The native result and exact managed group are read before and after independent
device proof. Source/controller checks also bracket proof and publication. Task
completion changes, native reconfiguration, replaced Nodes, stale sources or
revoked authority hold. Proof receives private operation/target copies and cannot
rewrite the admitted journal. An atomic status update publishes the original
operation as Verified together with its `lan-link-delete` application, preserving
the original ADD application. Chronological effect checks reject ambiguous or
stale publication. Both LAN actions share this publication implementation and
the existing group persistence contract.

Receipt publication does not release claims. Existing completion publication,
each device worker's lease release, both manager settlements, journal retirement
and reservation retirement remain separate required transitions. A lost status
acknowledgement preserves the written receipt and never triggers another native
DELETE.

## Qualification

[Focused proof/parser and atomic tests](lan-link-delete-proof-focused.log) cover
both valid and adversarial worker/CLI evidence, offline read reuse, stale results,
wrong identities, output errors, source/native/managed/task races, missing proof
or members, lost status acknowledgement and retained completion fencing. Explicit
fake proof callbacks isolate atomic publication races; independent worker-envelope
validation is tested separately.

A composed [worker settlement test](lan-link-delete-proof-workers.log) uses native
HTTPS fixtures, actual DeviceOperation evidence validation, real shared receipt
and completion functions, and the actual reservation-worker reconciler with a
RequireExisting FamilyLeaser against fake Kubernetes. Each retained canonical
lease holder is actually released by that worker code. The maintenance manager's
completed device/session cleanup is explicit fixture evidence, validated by the
real settlement publisher. Partial release or missing manager settlement cannot
clear the journal. Both original reservations are retired only after settlement;
both ADD/DELETE effects survive and native submission count stays unchanged.
This is stronger than assigning Released status directly, but is not deployed
actor/hardware qualification.

See [full affected race regressions](lan-link-delete-proof-regression.log),
[runtime contracts](lan-link-delete-proof-runtime.log),
[read-only lab prerequisites](lan-link-delete-proof-live.log), and
[CVK cluster readiness](lan-link-delete-proof-cluster.log).

## Remaining work

Deleted-state idempotency and re-add, bounded public DELETE Apply/recovery,
mixed-action/full-model composition, installed actors and actual post-DELETE CLI
qualification remain pending. Legacy/adoption paths and wider model/request-budget
qualification are also incomplete. No native LAN deletion was sent to the lab
by these changes. The existing CVK cluster API still returns EOF.

The focused proof/parser/atomic suite passed in **46.318s**; the added actual
worker-release/settlement composition passed in **7.987s**. Runtime/RBAC checks
passed in **3.827s** and fresh read-only native prerequisites in **4.960s**.
Full affected race regressions passed; exact package results are preserved in the
linked regression log. Strict documentation and whitespace validation also pass.
