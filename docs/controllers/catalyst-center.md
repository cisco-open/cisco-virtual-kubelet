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

Redirects are rejected for login, reads and writes. Only read requests with a
401 refresh the session once; 403 responses do not trigger another login, and
mutating POST requests are never retried automatically. Remote error bodies
are omitted while HTTP status classification is preserved. Status publication
uses direct Kubernetes reads and rejects stale UID/generation bindings, paused
controllers and terminating controllers. Enabled device adoption or an API
version other than `v1` is rejected rather than silently ignored.

## SWIM direction

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

Until that operation resource is added, live validation is deliberately
read-only: authentication, device inventory, and image inventory are tested
against the appliance without starting a distribution or activation.
