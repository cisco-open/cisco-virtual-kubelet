# SDA transit post-task device-read evidence

This phase connects fresh independent worker reads to the retained SDA recovery
flow. It supersedes the [journal phase](sda-transit-journal.md)'s immediate
convergence-unavailable status: Verifying now first waits for every CP worker's
fresh result. It does **not** treat completed reads as semantic transit
convergence, publish a completion receipt or release reservations.

## Worker lifecycle and scope

The connector uses existing `DeviceOperation` workers and the common device-proof
builder/reader. It opens no device transport and obtains no device credentials.
The fixed query plan is `show version` followed by `show lisp session all`.
The [Cisco IOS-XE command reference](https://www.cisco.com/c/en/us/td/docs/switches/lan/catalyst9500/software/release/17-12/command_reference/b_1712_9500_cr/cisco_sd_access_commands.html)
documents the latter as reliable-transport session information including all
session states. That scope alone cannot attest a complete transit configuration
or its intended relationships. The existing IOS-XE command allowlist accepts
the fixed plan; actual device-worker transport remains separately unqualified.

Reads cover the complete admitted group only after the original successful
root/tree completion boundary. Source, owner, exact journal, group membership and
current authorization are checked through existing helpers. Complete native/task
observations bracket the batch; between individual reads the source/owner/journal
and full group authority are repeated without a full native inventory pass per
member. Native/query/result identity and task completion time join the existing
controller/operation/group/task/worker fingerprint. One stable request per
member/worker/result avoids duplicate pending reads; the existing terminal TTL
cleanup remains responsible for lifecycle.

Only manager-bound, exact-spec, current-generation successful results with fresh
times, the expected owner/worker and complete nontruncated/nonredacted outputs
are consumed. Independent chassis serial must match the admitted native device.
Changed native result IDs or completion/query contexts cannot reuse old reads.
Partial member sets return no evidence. Raw session output stays private in
memory; formatting wrappers suppress it. Underlying DeviceOperation status uses
the existing operations retention/access model, not a new store.

## Public state boundary

Retained Accepted/acknowledged Unknown recovery still reaches Verifying through
the original task/readback proof. Subsequent recovery reports
`GroupDeviceEvidencePending` until all CP reads are available. With a complete
fresh bound set, it reports `GroupConvergenceVerificationUnavailable`: semantic
role/configuration/session expectations are still missing. All reservations,
journal identity and native submission count remain unchanged. Arbitrary
nonempty session text is deliberately not accepted as convergence proof.

## Verification

- [Race regression](sda-transit-device-evidence-regression.log) passes in 24.725
  seconds: 98 SDA subcases (prior 73 plus 25 evidence cases), three standalone
  checks for taskless Unknown, fixed query/allowlist contract and the public
  all-member read boundary, plus adjacent transit/fabric/group cases.
  Evidence cases cover pending/partial results, missing members, wrong worker/
  owner/command/serial, generation/time failures, truncation/redaction/artifacts,
  source/native/task/authority races and native result replacement. Both ordinary
  session-shaped text and opaque text remain diagnostic inputs only. No receipt,
  phase advance, release or native replay follows collection.
- [Real private API envelope](sda-transit-device-evidence-api.log) passes in 8.284
  seconds. It verifies a stable request's real UID, creation/generation, separate
  simulated manager metadata and worker status, chassis parsing, truncation
  refusal and changed-query/generation rejection. Controller/group inputs are
  fixture identities; this is not installed admission or a full real-API SDA
  device-worker cycle. Earlier real source/group persistence qualification remains
  in [journal evidence](sda-transit-journal.md).
- [Foundations](sda-transit-device-evidence-foundations.log): shared controller/
  group, ND, device-operation/diagnostic, software lifecycle, upgrade and GNOI
  checks pass; unchanged packages may use cached results.
- [Authenticated lab prerequisites](sda-transit-device-evidence-native-prerequisites.log)
  still find no wired-provisioned records on `.100`, `.101` or `.103`. Reads
  succeed with zero product writes. No CLI command or native SDA creation is
  performed against those devices by this qualification.
- [Final source/upstream fingerprints](sda-transit-device-evidence-sources.json)
  and [strict documentation build](sda-transit-device-evidence-docs.log).

## Remaining convergence and execution work

A verified semantic contract must relate the intended SDA mode, CP roles,
configuration and expected relationships to independent native device state.
Session collection alone does not supply that contract; empty or arbitrary
session output does not qualify it. Semantic completion, durable receipts and
reservation settlement/release remain mandatory before new-intent activation.
Mixed-model/provisioning sequencing, alias/FQDN projection, automatic Prepared
submission, updates/consumer footprints, native ownership/destruction, installed
actors and accepted live SDA execution remain pending. Full pinned NetAsCode
parity is incomplete.
