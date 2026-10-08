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

These sections correspond to the upstream Catalyst Center data model. The
adapter keeps SWIM as an operational capability rather than inventing a
Network as Code section for it.

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

## SWIM direction

Catalyst Center SWIM has distinct import, distribution, activation, and task
polling operations. The client contains the distribution, activation, and task
contracts, but a Kubernetes operation resource is still required before
mutating SWIM actions are exposed to users. The target preflight code already
requires a persisted IOS XE `CiscoDevice`, its immutable physical identity,
and a unique reachable Catalyst Center inventory record with the same serial
and management address. Image selection requires an imported image UUID.

The operation controller should be added in the same adapter worker and use
the existing `devicecoordination.MutationLeaseFamily` and namespaced device
key. It must persist the target CiscoDevice UID, serial, image UUID, phase,
task ID, and task acceptance state before advancing; acquire the lease before
distribution and hold it through activation and post-upgrade verification.
The existing IOS XE gNOI upgrade and rollout controllers must see the same
lease, so Catalyst Center and gNOI cannot upgrade one device simultaneously.
If a submission times out before an API task ID is recorded, reconciliation
must stop in an explicit ambiguous state and require task reconciliation;
blind POST replay risks duplicate activation. Successful Catalyst Center task
completion is not final success: verify device software version and identity
through the established device inventory/telemetry path and honour existing
rollout budgets before marking the operation complete. SWIM stays unsupported
in controller status until this durable path and its RBAC/CRD are installed.

Until that operation resource is added, live validation is deliberately
read-only: authentication, device inventory, and image inventory are tested
against the appliance without starting a distribution or activation.
