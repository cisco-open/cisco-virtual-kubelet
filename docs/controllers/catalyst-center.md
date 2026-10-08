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
  These methods are not wired to a public operation or exposed as an available
  controller capability.

The adapter uses the existing CVK REST transport for TLS, rate limiting,
request execution, and error redaction. Credentials are read from the worker's
projected `username` and `password` files. Tokens are held only in the worker
process and are not written to Kubernetes status or logs.

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
discarding free-form device output. This is an internal preflight facility;
it is not yet connected to production mutation admission.

Redirects are rejected for login, reads and writes. Only read requests with a
401 refresh the session once; 403 responses do not trigger another login, and
mutating POST requests are never retried automatically. Remote error bodies
are omitted while HTTP status classification is preserved. Status publication
uses direct Kubernetes reads and rejects stale UID/generation bindings, paused
controllers and terminating controllers. Enabled device adoption or an API
version other than `v1` is rejected rather than silently ignored.

## SWIM direction

### Execution selection and task engine

The internal `softwarelifecycle.UpgradeExecution` contract distinguishes
`Direct` from `CatalystCenter`. Omission resolves to `Direct`, preserving the
existing gNOI choice. A controller execution must pin its namespace, name and
Kubernetes UID. Switching methods or controller incarnations requires a new,
separately authorized operation; an outage never causes automatic fallback.
This contract is not yet exposed as an `IOSXESoftwareUpgrade` manifest field.

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

The executor is tested with simulated API, persistence and admission failures,
including lost activation receipts. **It is not registered in the worker yet.**
Its storage, authority and verification interfaces intentionally have no
permissive production defaults. The outstanding integration is a Kubernetes
operation API/store and an authenticated bridge to the existing manager/device
admission and evidence paths. The in-memory store exists only in tests.
Existing direct upgrade manifests continue through the existing reconciler;
there is no usable Catalyst Center upgrade manifest in this build yet.

### Admission integration still required

Catalyst Center SWIM has distinct import, distribution, activation, and task
polling operations. The client contains the distribution, activation, and task
contracts, but a Kubernetes operation resource is still required before
mutating SWIM actions are exposed to users. The target preflight code already
requires a persisted IOS XE `CiscoDevice`, its immutable physical identity,
and a unique reachable Catalyst Center inventory record with the same serial
and management IP address. This currently requires literal IP addresses;
hostname-to-device identity binding is not implemented. These local checks are
not wired into an operation controller and are not mutation authorization.

The product operation controller belongs in the adapter worker, but it must
obtain admission through CVK's existing device coordination authority. Reusing
`devicecoordination.MutationLeaseFamily` is necessary, but is not sufficient:
a remote SWIM task can continue after a worker exits or a Lease expires.
The implementation must integrate with the same durable mutation claims,
prepared-state checks, maintenance/drain controls, topology reservations and
uncertain-operation recovery used by the gNOI upgrade flow. It must use the
canonical CiscoDevice identity and the existing Lease's namespace and key;
creating a second Lease in the controller namespace would bypass coordination.
Managed admission also binds authorized worker identities and pre-created
Leases, so adding generic Lease RBAC to this adapter would not implement that
contract. The current read-only worker role is intentionally retained.

Persist the controller and CiscoDevice UIDs, serial, image UUID, operation phase
and mutation claim before dispatch; record the task ID immediately on acceptance.
Renew the fence throughout execution and preserve it when remote outcome is
uncertain, including after cancellation, deletion or worker restart. Do not
release it merely because a task polling deadline or Kubernetes Lease expired.
If a submission times out before an API task ID is recorded, reconciliation
must stop in an explicit ambiguous state and require task reconciliation;
blind POST replay risks duplicate activation. Successful Catalyst Center task
completion is not final success: verify device software version and identity
through the established device inventory/telemetry path and honour existing
rollout budgets before admitting mutation. SWIM stays unsupported
in controller status until this durable path and its RBAC/CRD are installed.

The default live test reads health and inventories. An additional opt-in test,
`TestLiveCatalystCenterSWIMReadiness`, consumes persisted readiness receipts from
`CATC_READINESS_RECEIPTS` and optionally checks product associations for the
comma-separated `CATC_TEST_IMAGE_IDS`. It never starts or replays a POST.
The receipt file maps management addresses to `device_id`, `started_at` (Unix
seconds), and `receipt.response.taskId`. Receipts expire after 15 minutes for
qualification. An expired or unsuccessful run requires a new, deliberately
submitted readiness check; rerunning the test does not submit one.

[The 8 October lab record](../evidence/catalyst-center-2026-10-08/README.md)
records the separately authorized readiness jobs and a site-scoped golden-image
assignment. Distribution and activation have not been qualified or deployed.

### API profile work required before deployment

The lab appliance reports 3.2.3. Cisco marks the old distribution/activation
endpoints used by the current internal executor as
[sunset](https://developer.cisco.com/docs/catalyst-center/api-changelog/).
Do not simply substitute URLs: modern
[distribution](https://developer.cisco.com/docs/catalyst-center/distribute-images-on-the-network-device/)
and [activation](https://developer.cisco.com/docs/catalyst-center/update-images-on-the-network-device/)
use object payloads and report workflow progress through
`networkDeviceImageUpdates?parentId=...`. Modern activation can also distribute
images, so its mutation claim must cover that behavior. Pin the selected API
contract in the durable operation and test its child-workflow outcomes; never
retry an ambiguous POST through the alternative API contract.
