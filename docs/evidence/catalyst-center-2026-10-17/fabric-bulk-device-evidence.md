# Bulk fabric-device worker evidence

Public recovery of an acknowledged bulk fabric-device operation now collects
fresh device-worker observations for the complete admitted incoming, peer and
parent footprint. It reuses the existing DeviceOperation envelope, manager-owned
worker binding, chassis identity check and reservation authorization. A shared
batch reader serves this path and wired bulk provisioning; existing wired names,
digest inputs and protocol annotations remain unchanged. SDA transit and fabric
bulk use the same read-only command list. This change adds no CRD, transport,
RBAC role or alternate device session.

## Correlation and retained authority

The collector requires a retained Verifying operation, original source and owner,
successful complete task tree, stable actual memberships and an unchanged managed
device/Node footprint. Deferred parent proposals cannot become device evidence.
Requests begin after the exact native task-completion boundary and cover every
admitted device, including peers outside the incoming batch. The query plan is
copied before callbacks and requires independent chassis observation first.

Read identities bind controller, operation, group, task, completion time, current
device-worker incarnation, post-task native membership/dependency digest and the
complete command plan. A changed plan or native membership cannot reuse an older
result. Only the manager supplies protected worker bindings. Failed, unbound,
stale, foreign, truncated, redacted or artifact-only results remain unusable.
Complete native/source/managed observations bracket the read batch; per-device
authorization repeats between reads. Raw outputs remain private and have opaque
formatters.

Public recovery reports `GroupDeviceEvidencePending` while worker observations
are unavailable. Once fresh results arrive, it still reports
`GroupConvergenceVerificationUnavailable`: nonempty CLI and matching chassis
identity do not establish fabric role/configuration convergence. It does not
publish a Verified application receipt, settle claims, release reservations or
dispatch another native fabric mutation. New public bulk dispatch remains gated.

## Qualification

The [validation record](fabric-bulk-device-evidence-validation.json) pins final
source hashes, completed logs, binary hash and observed lab prerequisites.

- Race regressions: **25 families / 230 subcases**, 65.373 seconds. Coverage
  includes root/zone footprints, peer omission, command changes, worker provenance,
  stale/partial output, source/native/member/task races, restart observation and
  refusal to treat collected output as semantic proof.
- Real API-server checks: **seven families / five named subcases**, 69.947 seconds.
  These qualify DeviceOperation UID/generation, metadata/status separation,
  query-spec replacement and output validation, plus existing SDA identity and
  journal envelopes. Manager/worker writes and physical/native actors are
  simulated; this is not installed multi-device fabric qualification.
- Nexus Dashboard regressions: 4.868 seconds. Selected GNOI/SWIM controller
  regressions: 6.163 seconds. No new upgrade hardware result is claimed.
- The finalized production worker builds for Darwin/arm64 without test helpers.
  It was not installed in the lab by this change.

Live prerequisite reads authenticated successfully and found **zero fabric sites**
in Catalyst Center and **zero CiscoDevices** in the isolated qualification cluster.
No fabric was created and no device configuration, demo-cluster recovery or
physical bulk operation was performed. Live fabric/site and device-worker cluster
details have been requested for later qualification.

## Remaining work

Role-aware semantic convergence checks must compare device evidence with the
declared fabric outcome across the complete footprint. Their accepted proof must
then feed the existing atomic receipt and reservation-settlement path before new
public bulk dispatch is enabled. Full root-to-zone/chunk lifecycle, installed
actors, mixed border roles, whole-model composition and live fabric qualification
remain pending. This is a concrete evidence-collection step toward pinned model
support, not completed `catalystcenter_fabric_devices` parity.
