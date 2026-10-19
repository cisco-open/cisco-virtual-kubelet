# Catalyst Center alpha checkpoint

Status: **Alpha; partial NetAsCode parity**. Assessment date: **19 October 2026**.
This checkpoint is intended for development, review and controlled lab use. It is
not a declaration of production readiness or complete controller support.

## Scope and compatibility

The Catalyst Center adapter consumes the pinned NetAsCode module **0.5.0**, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`. All **11 sections and 93 distinct
Terraform resource types** are inventoried. Recognition is complete; complete
read/plan/apply/prune parity is not. No whole-section parity flag is promoted by
this checkpoint. A resource with a working Create path may still lack fields,
defaults, ownership, recovery, deletion or installed-device qualification.

The adapter uses the existing controller worker, descriptor registry, REST
transport, credential projection and managed device mutation authority. SWIM
continues through the managed upgrade/handoff path; direct-device gNOI remains
a separate selectable backend. Product-specific mappings stay in this adapter.
The branch also extends shared reservation, admission, configuration lifecycle,
RBAC and workload checks. Those changes require review and regression coverage
for ND and the device providers; adapter separation alone does not prove isolation.

At this assessment, [PR #204](https://github.com/cisco-open/cisco-virtual-kubelet/pull/204)
targets `pr/moskrive/nd-support`, whose
[PR #203](https://github.com/cisco-open/cisco-virtual-kubelet/pull/203) remains open.
Review against that dependency. Before merging into `main`, merge the ND
dependency or explicitly agree the stacked merge strategy, then validate the
result against the current destination. Do not silently rebase, retarget or
consolidate the ND implementation as part of this checkpoint.

## What is present

- Health and inventory integration, explicit projected credentials, session/TLS
  handling and managed Catalyst Center SWIM, with bounded lab qualification.
- Offline pinned-model inventory, reporting, ordered model/default composition,
  qualified YAML resolution and explicit managed-site selection.
- Bounded configuration paths for sites, network settings, IPAM, templates,
  profiles, authentication, inventory and wireless resources. Other workflows
  have resource-specific prerequisites and restrictions, not blanket support.
- Shared write-ahead journals, original-task observation, independent readback,
  source/owner binding and device reservation protocols. Ownership, private
  comparison, pruning and recovery are implemented to different depths by resource.
- Unit/race tests, real temporary Kubernetes API tests, installed read-only worker
  evidence and isolated native API qualifications. These are separate evidence
  classes; a fake client or temporary API server does not qualify an installed
  worker or a physical-device operation.

Use the [resource ledger](catalyst-center-resource-parity.md) for individual
operations and restrictions, the [runtime guide](catalyst-center.md) for execution
contracts, and the [parity reference](catalyst-center-parity.md) for detailed
implementation history. Historical evidence describes the source and environment
captured at that time; it is not evidence that every later revision was deployed.

## Alpha operating contract

Use isolated, explicitly selected lab resources and one configuration owner per
scope. Existing Terraform ownership does not transfer merely because CVK imports
the same model. Unsupported fields, missing dependency evidence, ambiguous tasks,
unavailable comparison keys and conflicting device reservations must refuse or
retain their fence. Do not clear journals, status receipts or mutation leases to
force progress after an uncertain outcome.

Mutation is selected through explicit manifests, approval/policy and the existing
runtime/admission gates; this is not a blanket report-only adapter. Review every
Apply/Delete example against its documented qualification. SWIM may reboot a
device. Configuration definitions and their deployment to physical devices are
different operations, with different authorization and proof requirements.

Install matching CRDs, Helm admission/RBAC policies, manager and workers from the
same checkpoint. Reservation and metadata-handoff paths depend on API-server
enforcement, not merely client validation. The managed admission qualification
uses Kubernetes **1.35**; do not infer compatibility with older installations.
Existing deployment defaults do not opt every controller or device into these
operations.

Pause new work before an upgrade or rollback and inspect outstanding operations
and retained reservations. Keep the matching runtime available to observe or
settle acknowledged work. Back up cluster state under the operator's normal
process. Downgrade across newly introduced status/CRD protocols is unqualified;
do not remove CRDs, truncate receipts or drop admission policies as a rollback.

## Acceptance for an alpha merge

An alpha merge may carry documented missing capabilities. It must not carry a
known authorization bypass, destructive unqualified default, secret disclosure,
replay of acknowledged uncertain work, or a failing required regression gate.
Incomplete operations must remain rejected or explicitly bounded by their
existing opt-ins and evidence; a roadmap is not an exception to these rules.

Before merge, require:

1. A reviewed publication inventory containing source, tests, schemas, examples,
   licenses and sanitized evidence, with no appliance credentials or local backups.
2. Build, vet, formatting, recursive race tests, chart rendering, documentation
   and release-contract checks on the proposed source revision.
3. Real Kubernetes API qualification of the new configuration/admission paths;
   existing ND, direct-device and SWIM regressions must remain intact.
4. Green required CI checks on the actual PR head and destination, review of the
   shared security boundary, and resolution of the ND merge dependency.
5. Explicit acceptance of this alpha boundary and the remaining
   [full-parity roadmap](catalyst-center-roadmap.md).

The [checkpoint validation record](../evidence/catalyst-center-2026-10-19/alpha-checkpoint.md)
distinguishes checks run for publication from earlier live qualification.
The new controller-alpha jobs do not automatically become branch-protection
requirements. Maintainers must verify their results and the destination's
required-check configuration before merging; this checkpoint does not change
repository protection rules or enable automatic merging.

## Completion reporting

Track four separate outcomes: inventory, implementation, lifecycle correctness
and qualification. Inventory is **93/93**; this is not a completion percentage
for the goal. All eleven sections still have open gates. The resource ledger
contains historical and partial results, so counting mentions of “passed” or
“pending” cannot produce a valid overall percentage. Close the roadmap gates
with versioned evidence before advertising full parity.
