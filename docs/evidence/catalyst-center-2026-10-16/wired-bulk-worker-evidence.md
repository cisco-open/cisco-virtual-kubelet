# Wired bulk provisioning: post-task device-worker evidence

This adds the worker observation stage needed for `catalystcenter_provision_devices`.
It does **not** turn raw CLI output or compliance reporting into a final device
convergence proof, and does not enable public bulk dispatch.

## Shared implementation

The existing LAN-link `DeviceOperation` envelope now lives in
`config_group_device_evidence.go`. LAN add/delete keep their existing names,
binding annotation, digest inputs, TTL, worker validation and semantic checks.
A golden pre-extraction identity protects pending LAN requests across restart.
Chassis serial parsing is also shared; no new credentials, SSH client, command
executor, reservation, worker or journal implementation was introduced.

The private bulk collector requires the exact current Verifying operation,
original source/config/controller revisions, one inventory owner, complete
reservation membership and live caller-supplied authorization. It observes the
whole native task tree and original post-task provisioning footprint before
creating or consuming any worker result. Task completion, managed identity and
native state are checked again before returning evidence.

Every admitted native ID must have a bounded query plan starting with
`show version` and at least one further `show` command. The connector applies
basic envelope bounds; the existing DeviceOperation worker retains its full
platform command policy. It never supplies protected worker annotations.
The existing manager binds each read request to the device worker.

Read identities include the controller, original operation/group/task, device
and worker incarnation, exact completion boundary, all native post-task record
UUIDs and complete query plan. A missing result remains pending under a stable
name. Changed queries/records cannot consume an older result. Reads are not
created until the API timestamp precision can establish a post-task boundary.

Consumption requires matching owner/spec/annotation, real object identity,
complete manager binding, current successful generation, Ready condition,
post-task creation/start/completion ordering and a result at most five minutes
old. Missing, failed, stale, replaced, truncated, redacted, artifact-only,
wrong-command or wrong-worker results are refused. Independent chassis evidence
must match the admitted physical serial. Output remains private and its default
formatters do not print CLI content.

The collector returns evidence only. It writes no application receipt,
completion, grant, claim, operation phase or release marker. A test deliberately
uses nonempty but semantically meaningless output, then confirms that the final
receipt API still rejects a missing convergence verifier.

## Verification

- [Final safety suite](wired-bulk-worker-safety.log): bulk all-member worker
  evidence, post-task boundary, golden LAN identity and LAN proof/CLI tests.
  Includes source/task/native/worker/authorization races and restart stability.
- [Broader regression suite](wired-bulk-worker-regression.log): race-enabled LAN
  add/delete, bulk and shared configuration tests.
- [Private API-server qualification](wired-bulk-worker-api.log): actual CRD,
  UID/timestamps, generation and status-subresource behavior. A pending read
  retains one identity; simulated manager/worker status is accepted, then
  redaction and spec replacement are refused. The first fixture lacked the
  existing CRD's mandatory condition reason; the corrected fixture passes.
  Actor writes are simulated; this is not installed RBAC/admission qualification.
- [Foundation regressions](wired-bulk-worker-foundations.log): adapter registry,
  ND, shared controller configuration, software lifecycle/upgrade and the actual
  DeviceOperation worker package suites.
- [Read-only native compliance inspection](wired-bulk-worker-native.log): all
  three switches were queried using the restored tunnel. NETWORK_SETTINGS is
  COMPLIANT/SUCCESS but last checked at 2026-10-15T23:00:02Z for `.100` and `.103`,
  and 2026-10-09T22:23:48Z for `.101`. NETWORK_PROFILE is absent on all three.
  These reports fail the fresh observation boundary. No compliance refresh,
  remediation, task submission or device mutation was performed.

## What remains

A subsequent [declared-network stage](wired-bulk-declared-network-intent.md) adds
original-source policy guards and scoped NTP/DNS semantic comparison. It does
not close the complete rendered-intent or whole-device proof gates below.

A resource-specific verifier must bind the **original immutable rendered
configuration intent** before submission and compare fresh worker observations
against it after the exact native completion. Current controller policy cannot
silently replace the intent of an already accepted operation. A task success,
fresh inventory, chassis match, nonempty CLI output or an old compliance report
does not establish this semantic comparison.

The new collector is private and not connected to public recovery as an
automatic success provider. Public preparation/submission, operation-bound
rendered intent, semantic convergence, installed manager/device-worker
qualification and accepted native POST/PUT cycles remain pending. No live
post-task DeviceOperation or device-convergence success is claimed. All existing
group fences remain held until the established final receipt and settlement
contracts are satisfied. The 93-resource ledger remains partial.
