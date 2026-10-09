# Catalyst Center controller adapter

The `catalyst-center` adapter connects a `NetworkController` to the Catalyst
Center Intent API. It uses the controller-neutral worker, credential projection,
TLS policy, session rotation, status conditions, and descriptor registry shared
by the other controller adapters. It does not change the Nexus Dashboard
adapter.

The descriptor is aligned to the Catalyst Center Network as Code model:

```text
format:  netascode-catalyst-center
stripe:  catalyst_center
sections: sites, network_settings, network_profiles, fabric, templates,
          inventory, wireless, lan_automation, system_settings
model version: 0.5.0
```

These sections correspond to the upstream Catalyst Center data model. Version
`0.5.0` is the selected [NetAsCode release baseline](https://netascode.cisco.com/docs/changelogs/catalystcenter/changelog/),
not a claim of full schema validation. There is no `NetworkControllerConfig`
intent reconciler in this adapter yet. Configuration validation, drift reports
and apply remain unimplemented. SWIM is a separate operational workflow and
does not introduce a new Network as Code section.

## Current capabilities

- Health authentication through `POST /dna/system/api/v1/auth/token`.
- Read-only inventory through `GET /dna/intent/api/v1/network-device`.
- Read-only SWIM image inventory through
  `GET /dna/intent/api/v1/image/importation`.
- Internal SWIM distribution and activation request contracts, including
  asynchronous task IDs and task polling through `/dna/intent/api/v1/task/{id}`.
  Managed rollouts now delegate these operations through a durable
  `CatalystCenterSWIMHandoff`; final success requires native XE verification.

The adapter uses the existing CVK REST transport for TLS, rate limiting,
request execution, and error redaction. Credentials are read from the worker's
projected `username` and `password` files. Tokens are held only in the worker
process and are not written to Kubernetes status or logs.

Co-locating SWIM with managed XE devices requires credential Secret references
for every CiscoDevice in that namespace. The namespace RBAC audit accepts only
the canonical, live Catalyst Center worker binding and its exact compiled SWIM
role; inline device passwords, stale ownership, extra subjects, and broader
permissions remain blocking.

Device and image inventories use bounded, one-based pagination. Invalid,
missing, null or repeated pages fail the refresh without publishing a partial
inventory as successful. Image fields follow the
[Intent API image schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/get-software-image-details/),
including `imageName`, `isTaggedGolden` and `imageIntegrityStatus`.
An image UUID lookup establishes presence only; it does not establish image
integrity, device compatibility or authorization to activate.

The adapter client also supports the modern device-image details and readiness
APIs. Product association binds the device UUID and management address to the
controller's exact product identifier, including supervisor where applicable,
then matches that identifier to the imported image's `mdfId`. The human-readable
family name and the catalogue's sometimes incomplete PID list are insufficient
on their own. Readiness results reuse the bounded inventory pagination helper
and are filtered by device and operation.

Readiness qualification requires matching task/device identities, fresh completed
validation evidence and explicit success for every returned check. Missing,
conflicting, warning, skipped or partial results fail qualification. A successful
parent task with no validation records is not a pass. The decoder accepts both
the documented top-level status and the 3.2.3 `resultDetails` STATUS entry, while
discarding free-form device output. The managed SWIM handoff runs these checks
before distribution and again before activation.

A completed but unsuccessful readiness run can be replaced after an explicit
parent reauthorization, such as pause/resume after verified remediation. The
new device-worker grant must differ from the previous one, at least one minute
must have elapsed, and each stage permits at most three submissions. Prior task,
claim, stage and submission-time receipts are retained in `readinessHistory`.
The new check uses the same write-ahead dispatch and fresh admission checks.
In-flight checks and uncertain submissions are never replayed. A repeated task
ID is rejected; retries do not waive any readiness check or activation gate.
This supports supervised recovery. Opt-in manifest-driven cleanup and inventory
synchronization are described in [SWIM remediation](catalyst-center-remediation.md).

Redirects are rejected for login, reads and writes. Only read requests with a
401 refresh the session once; 403 responses do not trigger another login, and
mutating POST requests are never retried automatically. Remote error bodies
are omitted while HTTP status classification is preserved. Status publication
uses direct Kubernetes reads and rejects stale UID/generation bindings, paused
controllers and terminating controllers. Enabled device adoption or an API
version other than `v1` is rejected rather than silently ignored.

## Managed SWIM execution

### Execution selection and task engine

The internal `softwarelifecycle.UpgradeExecution` contract distinguishes
`Direct` from `CatalystCenter`. Omission resolves to `Direct`, preserving the
existing gNOI choice. A controller execution must pin its namespace, name and
Kubernetes UID. Switching methods or controller incarnations requires a new,
separately authorized operation; an outage never causes automatic fallback.
Managed rollout image sources select `catalystCenter` explicitly; the manager
copies that binding into the generated `IOSXESoftwareUpgrade` leaf.

The adapter now has an internal SWIM task executor with this sequence:

```text
Pending -> distribution claim persisted -> Distributing -> Distributed
        -> activation claim persisted -> Activating -> Verifying -> Succeeded
```

Each POST requires a durable mutation-authority claim, an atomic write-ahead
dispatch marker and a fresh authorization check. Task IDs are recorded before
polling. Restarting with a dispatch marker but no receipt enters
`OutcomeUnknown`, retaining the device fence and never resubmitting the POST.
Missing tasks and failed polls also cannot authorize replay. Activation needs
a completed distribution; task completion then requires fresh, matching device
identity/version evidence before success is persisted and the fence released.
Unsupported or incomplete operation records fail closed.

### Managed handoff

The XE software-upgrade reconciler owns the existing maintenance session,
rollout reservation, canonical device mutation Lease, and stage claims. It
creates a same-namespace `CatalystCenterSWIMHandoff` journal and publishes a
short-lived stage grant in the parent upgrade's `status.controllerHandoff`.
The Catalyst Center worker consumes that grant and updates only journal status.
It cannot write the parent admission, acquire another Lease, or read device
credentials. The dedicated worker role permits read-only Lease access in its
namespace; the first supported deployment therefore places the controller,
device and canonical Lease in the same namespace.

The controller checks uncached parent/controller/device identities, maintenance
acknowledgement, exact live Lease UID/holder, grant expiry, policy/control
revision, pause/cancel state, maintenance window, and network-evidence deadline.
Every distribution or activation also requires the parent's matching durable
mutation claim. Inventory checks bind the serial, management IP, controller
device UUID, image UUID/version and product association immediately before
submission. The requested image must be the device's sole golden SYSTEM image,
since that is the target of the qualified readiness endpoint. A second live
grant check follows those inventory calls.

The journal uses Kubernetes resourceVersion compare-and-swap. A persisted
submission marker without its receipt becomes `OutcomeUnknown` after restart;
it never authorizes another POST. Unresolved mutations remain in canonical
quarantine, including across Lease expiry and deletion requests. Cancellation
blocks new stages but allows observations of accepted work. Controller UID or
configuration-generation changes fence the journal for investigation.

After Catalyst Center reports activation success, the XE worker uses the
existing gNOI Verify and native lifecycle inventory paths to prove the pinned
version is running and committed. It writes identity-bound native evidence to
the parent; the controller then marks the journal successful. Only then does
the XE worker settle the parent and allow existing maintenance recovery to
release the fence. Successful journals remain as audit records, with their
retention finalizer removed.

Install the generated CRDs, chart RBAC/admission policies, and matching manager
and device/controller worker binaries together before using the new source.
Manager startup verifies the new admission policy's compiled contract. The
`rollout-controller-swim-v1` handshake and `CatalystCenterV1` execution marker
prevent old workers from interpreting controller work as Direct/gNOI work.
Existing URL/device-file/preinstalled sources keep their existing paths.

Use the controller rollout example at
`examples/configs/catalyst-center/swim-rollout.yaml` in the repository
through the existing plan/approve/execute workflow. Each controller source pins
one device UUID: target one device, or provide separately selected sources for
different device UUIDs. This first execution profile requires single-supervisor
IOS XE, `Reload`, explicit `rollbackOnFailure: false`, and no byte-pacing policy.
It does not silently substitute direct rollback, ISSU, cleanup or transfer
pacing. Campaign SHA-256 remains the administrator's imported-image audit pin;
controller UUID/version and product/readiness evidence do not independently
prove a vendor signature or the imported file checksum.

The default live test reads health and inventories. An additional opt-in test,
`TestLiveCatalystCenterSWIMReadiness`, consumes persisted readiness receipts from
`CATC_READINESS_RECEIPTS` and optionally checks product associations for the
comma-separated `CATC_TEST_IMAGE_IDS`. It never starts or replays a POST.
The receipt file maps management addresses to `device_id`, `started_at` (Unix
seconds), and `receipt.response.taskId`. Receipts expire after 15 minutes for
qualification. An expired or unsuccessful run requires a new, deliberately
submitted readiness check; rerunning the test does not submit one.

[The 8 October lab record](../evidence/catalyst-center-2026-10-08/README.md)
records the deployed `.101` upgrade from 17.18.03 to committed 17.18.04 through
Catalyst Center, including native verification, health soak and fence release.
Cleanup and controller inventory synchronization were supervised in that run.
The subsequent [automatic remediation qualification](../evidence/catalyst-center-2026-10-08/automatic-remediation/README.md)
records manifest-triggered archive deletion, independently observed free space,
and a correlated successful inventory synchronization on `.101`. That separate
same-version qualification stopped at an xFSU readiness warning; it is not
evidence of another completed upgrade or a version-changing automatic cycle.

### Modern API execution profile

The managed executor supports the pinned `networkDeviceImages-v1` profile.
It persists a readiness dispatch marker and receipt before distribution and
again before activation, requires the six qualified XE checks, and observes
the exact per-device workflow (task, device, operation, controller image version
and time range). Empty workflow results remain pending. Final success still
requires fresh device-side version verification. Contract selection is immutable;
the legacy route remains separate and is never used as a retry fallback.

An optional `transferFallbackAddress` in `imageSource.catalystCenter` qualifies only the
exact 3.2.3 warning detail pair reporting positive HTTPS/SCP reachability and
failed NETCONF transfer for that literal appliance IP. It does not suppress
flash, startup, image, xFSU or unknown warnings. Omission blocks all warnings.
This narrow manifest option is not proof that a live transfer works.

These transitions have race-tested simulated coverage, including lost responses,
missing receipts, missing workflows and partial readiness. Kubernetes storage
and the manager/worker handoff are implemented. The supervised 8 October live
run qualified distribution, activation, native committed-image verification and
maintenance release on `.101`. Automatic retired-archive cleanup and inventory
synchronization are implemented through the opt-in preparation policy; their
qualification limits are described in [SWIM remediation](catalyst-center-remediation.md).
The adapter advertises managed SWIM when image inventory is available;
that capability alone does not certify other releases, platforms or remediation
policies.

### Qualified API profile and current limits

The opt-in `standardReloadProfile: CatalystCenter323` pins Catalyst Center build
`3.2.3-75346.100` and uses the modern device-image APIs. A live 17.18.04 to
26.02.01 campaign completed through distribution, activation, reboot, commit,
native verification and maintenance release. See the
[qualification report](../evidence/catalyst-center-2026-10-09/standard-reload/README.md).
This qualifies normal reload on that build/path, not xFSU or other versions.
The legacy internal execution contract remains separate; an ambiguous modern
submission never retries through the legacy API.

Unknown readiness warnings remain blocking. The explicit reload profile may
classify only the observed, target-bound cross-version xFSU warning as
non-applicable. The separately selected `transferFallbackAddress` qualifies only
the exact positive HTTPS/SCP warning described above. Neither option suppresses
flash, startup, image compatibility or device eligibility failures.

## Manifest-driven preparation and recovery

Use [SWIM preparation and remediation](catalyst-center-remediation.md) and the
examples in `examples/configs/catalyst-center/` for the implemented contract.
The Catalyst Center source optionally pins an immutable administrator-owned
ConfigMap by name, UID and SHA-256 under `preparation`. The supported policy is
`RetiredCVKImageArchivesV1`; this is CVK operational policy, not a new NetAsCode
configuration section. No target manifest accepts arbitrary cleanup commands.

The device worker observes native state and can remove only exact retired CVK
image archives backed by invalidated preparation receipts. It verifies size,
digest, boot/install references, current/target image protection, empty application
inventory and cumulative file/byte limits before issuing a durable one-shot
removal. Catalyst Center then refreshes inventory, verifies the exact device's
sync completion and runs fresh readiness checks before distribution and again
before activation. A lost mutation response is reconciled from observations;
unknown outcomes retain the fence and are never blindly replayed.

Reserve both incoming archive and extracted-package space before distribution,
in addition to the pre-activation floor and headroom. If eligible archives cannot
satisfy that budget, the workflow stops. Application archives, installed packages,
route/VRF repairs and arbitrary file cleanup are outside this policy. Required
network configuration must converge separately through the existing drivers.

Automatic cleanup and inventory synchronization have separate live evidence.
The completed version-changing campaign used supervised application-archive
offloads and no-op automatic preparation; it does not establish a fully
unattended version-changing cleanup cycle. Recovery testing and the final merge
candidate must preserve this distinction.

### Cleanup ownership and protection

Catalyst Center documents
[automatic flash cleanup during distribution](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/3-1-x/user_guide/b_cisco_catalyst_center_user_guide_3_1_x/b_cisco_dna_center_ug_3_1_x_chapter_0100.html).
Its native behavior may remove unused image/package/configuration files until
space is available. This does **not** establish that its API supports CVK's
per-file allowlists, byte limits or retention exclusions. Capability validation
must establish those guarantees before a `CatalystCenter` cleanup action can
be admitted. A policy the selected executor cannot enforce is unsupported,
not permission to weaken the policy or fall back to a different executor.

If native cleanup cannot enforce the policy, use an explicitly selected,
separately coordinated device preparation action through the existing device
worker. Controller credentials remain in the controller worker. The upgrade
still executes through Catalyst Center; choosing a direct preparation action
must not silently change the upgrade executor or introduce another device
credential/session stack in the CC adapter.

Mandatory exclusions cannot be disabled by target manifests: active/committed
images and boot files, the selected target image, rollback images retained by
policy, files referenced by unresolved operations or valid preparation receipts,
and app-hosting packages/storage. An "inactive" install package is not
automatically safe to delete: it can be the rollback or a prepared target.
Do not translate `RemoveUnreferencedImageFiles` into blanket `install remove
inactive`, wildcard deletion or deletion of `.conf` files.

Bind each candidate to the device UID/serial, filesystem/member, exact path,
size/digest and inventory revision. Recheck references immediately before
dispatch while holding canonical device mutation admission. If files or
references changed, invalidate the plan instead of selecting replacement files
under the old authorization. Post-upgrade cleanup is a separate action after
successful verification and the required rollback/soak retention period.

### Integration with the existing upgrade flow

The logical sequence is:

```text
Observe -> Classify checks -> Plan authorized remediation
        -> Claim existing device mutation admission -> Execute
        -> Verify remediation -> Fresh readiness
        -> Admit distribution/activation -> Verify device and workloads
```

Remediation reuses existing canonical device coordination, maintenance policy,
topology reservations, manager/device identity binding and durable mutation
claims. It must not acquire an independent "cleanup lease" or use the controller
namespace as the device lock key. Apply drain requirements appropriate to the
action; do not assume that controller-owned distribution is only a byte copy.
The manager records the action plan before dispatch; the executor records its
task receipt and outcome. Status should expose the finding, action, executor,
candidate files, reclaimed bytes, attempts and fresh verification timestamps.

A lost response or expired timeout is an uncertain outcome: preserve the fence
and reconcile evidence, rather than replaying deletion or distribution. Known
remaining insufficient space stops the upgrade after the bounded action;
there is no unbounded cleanup/retry loop. A readiness retry is a new observation
job after a settled run, not permission to replay an ambiguous mutation.

Implement this first in the CC operational integration and use the existing
coordination hooks. ND and default Direct/gNOI behavior remain unchanged.
Qualify protected-file exclusion, insufficient-space-after-cleanup, changed
inventory, stale policy/controller/device bindings, duplicate submissions,
lost task receipts, restart recovery and concurrent Direct versus SWIM claims
before enabling mutation from these proposed manifest fields.

### Live test preparation

After replacing workers, wait for the manager to accept a fresh complete network
observation before submitting a rollout with network health enabled. A failed
planning attempt is retained; submit a new plan once its prerequisite is met.
Use `BlockIfRunning` for an empty target, or the existing policy-bounded `Drain`
workflow for workload migration. A replacement that cannot activate must block
PDB-aware drain; it is not evidence of a SWIM failure or permission to bypass
that gate. Cancellation must settle before another upgrade targets the device.

If cancellation leaves an unowned CVK maintenance taint, an infrastructure
administrator can request its removal by annotating the CiscoDevice with
`operations.cisco.vk/cancelled-drain-taint-release=<leaf-UID>/<session-token>`.
Use the exact retained operation UID and session token from
`status.maintenanceSession`. This is an explicit recovery action, not automatic
SWIM remediation. The manager requires a validated, cancelled, recovering
workload-drain session, an unexpired recovery deadline, all selected pods closed,
and a wholly idle, request-free canonical lease. Software mutation claims,
controller handoffs, pre-existing taints, and desired CiscoDevice taints prevent
this action. Only the exact `cisco.vk/device-maintenance=gnoi:NoSchedule` taint is
removed; unrelated taints and all lease/admission state are preserved. The
annotation remains as audit evidence and cannot match a different session.
