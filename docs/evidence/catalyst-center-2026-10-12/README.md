# Catalyst Center reporting and configuration execution qualification — 12 October 2026

## Scope and outcome

**PASS for bounded reporting and isolated site/network-setting API execution cycles.**
Full NetAsCode parity is not complete. Initial reporting tests made no writes;
the site execution qualification below created/updated/deleted only temporary
fixtures. No new manager/controller-worker image was deployed.

The branch's Linux AMD64 live-test binary ran on Ubuntu16 using credentials
obtained locally from the existing `cvk-live/catc-credentials` Secret. Values
were supplied to the process without printing them or writing them to evidence.
The existing live helper uses explicit lab-only TLS verification bypass.

```text
=== RUN   TestLiveCatalystCenterSitesReport
PASS: live sites=3 areas=1 buildings=1 floors=0; round-trip and synthetic drift; no configuration writes
--- PASS: TestLiveCatalystCenterSitesReport (0.19s)
PASS
```

The test authenticated to the existing `cvk-live/catc` endpoint, read the modern
Site Design API with the CVK client, verified complete parent relationships,
converted observed area/building attributes into resolved test intent and
obtained zero drift. A synthetic missing area produced Add; a local change to
a building address produced Update. Both differences were calculated locally;
the controller's configuration stayed unchanged.

The lab response contained three sites (Global, one area, one building) and
three managed devices. There were no floor objects in this snapshot. Floor
validation and comparison therefore have fixture coverage only, not live
qualification. Nor does this test establish fabric/wireless/LAN automation or
any create/update/delete capability.

## Automated checks

The added tests cover duplicate/ambiguous input, unsupported/unresolved site
fields, invalid parent/type relationships, missing managed observations,
partial HTTP responses, deterministic redacted differences, numeric rounding,
report-only request methods, and stale controller/config/ConfigMap revisions.
ConfigMap dependency-watch tests verify namespace/controller targeting.

The report reconciler is tested with a fake Kubernetes API and a TLS fake
Catalyst Center. That is separate from the real appliance reader/planner test.
A matching manager/worker deployment and real-API-server reporting test remain
needed before claiming in-cluster reporter qualification.

The remote test binary and log are under
`/tmp/cvk-catc-model-qualification/` on Ubuntu16. The local test was built from
the uncommitted branch working tree; it must not be attributed to the previous
PR head's CI result. Subsequent source-watch changes affect Kubernetes wiring,
not the site API/normalizer/planner exercised by that binary.

See the [resource parity backlog](../../controllers/catalyst-center-resource-parity.md)
for the remaining 93 resource-type apply gates.


## Network settings qualification

The same compiled branch client also passed these read-only live tests:

```text
TestLiveCatalystCenterNetworkSettingsReport PASS (1.32s)
sites=3 explicit=0 inherited=0 unset=9
complete reporting and synthetic drift; no configuration writes
TestLiveCatalystCenterSitesReport PASS (0.17s)
sites=3 areas=1 buildings=1 floors=0
```

All nine NTP/DHCP/DNS observations were unset. This qualifies modern endpoint
access, strict response parsing, site resolution, complete report assembly and
local synthetic drift. It does **not** qualify populated settings or inherited
settings live; those are covered by fake-API tests. DNS resolver ordering,
null-versus-unset behavior, missing fields, partial HTTP results and failure
following a successful read have regression coverage. Apply mode is rejected
before any controller access.

`go test -race ./internal/controlleradapter/... ./tools/cvk-netascode-migrate`
and targeted `go vet` passed. The live log is
`/tmp/cvk-catc-model-qualification/model-reports-live.log` on Ubuntu16.
The in-cluster deployment and all configuration mutation gates remain open.

## Timezone and banner extension

The subsequent branch qualification passed:

```text
TestLiveCatalystCenterNetworkScalarReport PASS (1.23s)
sites=3 explicit=1 inherited=2 unset=3
 timezone/banner model reports and synthetic drift; no writes
TestLiveCatalystCenterNetworkSettingsReport PASS (1.07s)
sites=3 explicit=0 inherited=0 unset=9
TestLiveCatalystCenterSitesReport PASS (0.19s)
```

Scalar qualification exercised NetAsCode named profiles and site bindings,
actual explicit/inherited/unset API observations, complete report assembly and
synthetic local changes. Aggregate state counts cover the six timezone/banner
reads; they do not claim every state was observed for each individual setting.
Missing and null fields, built-in versus custom banners, empty custom banners,
exact text comparison and redaction have additional fake-API coverage.

The full controller-adapter and migration-tool race tests and targeted vet
passed. These remain read/report capabilities. No configuration writes or new
in-cluster deployment were made. Source changes remain in the local working
tree and are not represented by the existing PR head's CI results.

## Telemetry profile extension

```text
TestLiveCatalystCenterTelemetryReport PASS (1.99s)
sites=3 telemetry groups explicit=5 inherited=10 unset=0
model round trip and synthetic drift; no writes
TestLiveCatalystCenterNetworkSettingsReport PASS (1.05s)
TestLiveCatalystCenterNetworkScalarReport PASS (1.17s)
TestLiveCatalystCenterSitesReport PASS (0.16s)
```

The live test converted Global telemetry observations to a NetAsCode named
profile, resolved site references and obtained zero Global drift. The area's
and building's inherited settings remained distinguishable from explicit
intent. A local boolean change produced one drift entry. This qualifies
read/report behavior for the lab's current settings, not external collector
configuration or device provisioning. External collector mapping, invalid
ports, missing/null booleans and server lists, partial HTTP responses and mixed
network/telemetry profile references also have fixture coverage.

Full controller-adapter and migration-tool race tests and targeted vet passed.
No worker image was deployed and no Catalyst Center configuration was changed.
All configuration write/ownership/recovery and in-cluster qualification gates
remain open; these source changes are local and uncommitted.

## AAA/ISE metadata extension

```text
TestLiveCatalystCenterAAAReport PASS (0.64s)
sites=3 AAA groups explicit=0 inherited=0 unset=6
model reporting; shared secrets excluded; no writes
TestLiveCatalystCenterNetworkSettingsReport PASS (0.89s)
TestLiveCatalystCenterNetworkScalarReport PASS (1.05s)
TestLiveCatalystCenterSitesReport PASS (0.18s)
TestLiveCatalystCenterTelemetryReport PASS (1.99s)
```

The appliance exposed both AAA groups as unset on each site. Live qualification
therefore covers API access, response parsing, profile bindings and differences
against explicit metadata intent. Populated AAA/ISE, inherited settings, server
changes, ISE PAN requirements, malformed observations and partial HTTP responses
have fixture coverage only. No live AAA configuration or credential validation
was performed.

Tests verify that returned shared secrets are excluded from observations and
status, secret-bearing intent fails before API access, apply mode fails before
API access, and successful metadata status explicitly disclaims shared-secret
comparison. Controller-adapter/migration race tests and targeted vet passed.
The first SSH execution connection closed before starting tests; the retry
completed successfully. Existing manager/worker deployments were unchanged;
source additions remain local and uncommitted.

## IPAM, global authentication templates and multi-section reporting

```text
TestLiveCatalystCenterAuthTemplatesReport PASS (1.12s)
global authentication templates=3; resolved model round trip and synthetic drift; no writes
TestLiveCatalystCenterIPAMReport PASS (0.23s)
global pools=0 reservations=0; synthetic pool/reservation Add report via real site reference; no writes
```

All earlier site, NTP/DHCP/DNS, timezone/banner, telemetry and AAA qualification
tests also passed in that run. The lab currently has no IPAM pools, so populated
pool/reservation matching, dual-stack merging, invalid containment and missing
managed fields have fixture coverage rather than live coverage. All three
global authentication templates were observed and round-tripped through the
pinned model mapping; a local Wake on LAN change produced a single difference.

Multi-section fake-Kubernetes tests cover per-section state, invalid later
intent making zero API requests, unselected source sections, and later API
failure suppressing all partial drift/current-success timestamps. Source
projection retains numeric precision and existing stale-identity/source guards.
The compiled lab binary exercised the pre-existing individual reporters and
new IPAM/authentication reporters; no new manager or worker was deployed.

This remains partial configuration reporting. The full-parity objective is
active; resource execution, ownership, recovery, remaining model families and
in-cluster deployment qualification are not complete. Work is local/uncommitted,
not covered by the existing PR head's CI status.

## Native execution journal qualification

Added an optional controller-owned section ledger and single-operation journal,
plus a helper coupling the persisted submission permit to one API callback.
Fixture tests exercise concurrent reservations and submission permits, worker
restart, config deletion/recreation, stale ConfigMap snapshots, missing task
receipts, request cancellation, lost Kubernetes acknowledgements, task acceptance
without verification, and recovery after pause/policy changes. Manager tests
verify deletion retains the journal even with no dependent manifest and resumes
normal cleanup once recovery clears it.

The existing controller-adapter race suites (including Nexus Dashboard), manager
suite and migration tool suite passed after the first journal addition. The
final Catalyst Center, manager and API race suites also passed after adding the
submission helper and timestamp. `go vet`, `git diff --check`, matching generated
Helm/standalone CRDs, and the strict MkDocs build passed. No journal-backed remote mutation or cluster rollout has been
performed; the new journal is not yet wired to a manifest apply reconciler.

## Site execution: live API cycle passed

[Captured successful output](site-execution-live.log) records the final 9.95-second
qualification on Ubuntu16. The CVK client created an isolated area, building and
floor, changed floor width from 10 to 12 meters, correlated terminal task
receipts, verified the full hierarchy, and deleted all temporary objects with
identity/child checks and post-delete reads. The runner log is also at
`/tmp/cvk-catc-model-qualification/site-execution-live.log` on Ubuntu16.

Two earlier attempts exposed real appliance contracts and produced fixes:

- Site names are limited to 40 characters. The initial overlong name was rejected
  by task `01a12ff9-acd8-7081-b5c3-b8e278c36eb0` with `NCGR10034`; inventory
  confirmed no corresponding site. Execution now rejects oversized names before
  issuing a request.
- The controller clock was approximately 76 seconds ahead of Ubuntu16. Exact
  receipt identity remains mandatory, with a bounded two-minute skew allowance;
  older/future-outside-bound timestamps still fail.
- Get Sites defaults floor geometry to feet. A 10m width was returned as
  32.8084ft. CVK now compares physical dimensions without treating equivalent
  response-unit representations as drift. The second attempt's three verified
  temporary IDs were explicitly removed before the successful final run.

Manifest-level tests exercise create, task correlation, read-back, convergence,
source/owner binding, repeated reconciliation, simulated worker restart, uncertain
receipt fencing and configured task timeout. The affected adapter race suites,
including ND and existing CC/SWIM tests, pass; API/manager suites passed after the
journal identity schema changes. Vet and strict documentation checks also pass.
The example `sites-apply.yaml` is parsed and compiled by a test.

This is live **resource API** qualification, plus fixture-backed manifest/journal
qualification. A matching in-cluster CRD/manager/worker rollout, live manifest
execution, explicit ownership transfer, automatic discovery of lost receipts,
prune/delete policy and the remaining model families are still pending. It is
not evidence of full model parity or current PR-head CI coverage.

## Network-settings execution: six live updates passed

[Captured successful output](network-execution-live.log) records the final
24.49-second run on Ubuntu16. The test created a fresh empty hierarchy, verified
its site operations, then applied and read back NTP, DHCP, DNS, timezone, banner
and builtin-collector telemetry at its area. A subsequent plan was empty. The
same run verified deletion of all temporary sites. Existing Global settings and
switch assignments were not changed. The remote log is at
`/tmp/cvk-catc-model-qualification/network-execution-live.log`.

The first run exposed controller normalization: child wired-collection and SNMP
trap settings matching Global were returned as inherited (`null`) despite a
successful PUT. Comparison now walks validated parent IDs for inherited fields,
using explicit observations at each ancestor. Incomplete/missing parents fail;
unset fields are not filled with assumed values. This behavior is shared by
reporting and apply. The earlier temporary hierarchy was removed after checking
its terminal task and exact IDs before the successful clean rerun.

Network-setting and site executors now supply resource semantics to one shared
journal state machine; there is no duplicated controller lifecycle, transport
or mutation retry loop. Tests cover all six settings across simulated restarts,
exactly one PUT each, no writes after convergence, payload nesting/order/empty
lists, unsupported intent, incomplete later reads, replaced sites, inherited
values and failed parent reads. Source revision changes release only Prepared
or Verified boundaries; accepted/uncertain operations remain fenced.

Affected adapter/migration race suites, vet, the unified `cisco-vk` build and
strict documentation checks passed. Both apply examples compile. External
NetFlow collector behavior has fixture coverage only; the live test used the
builtin collector. AAA/shared-secret and credential writes, IPAM writes, the
remaining model families, compound apply, prune/delete, ownership transfer and
matching in-cluster rollout remain pending. Full parity is not claimed.

### Native IPAM execution qualification

`ipam-execution-live.log` records the successful isolated qualification on
2026-10-12. The native executor created one IPv4 global pool and its site
reservation using the actual parent pool UUID, correlated both task receipts,
verified read-back and convergence, then deleted only the fixture identities.
Reservation, pool and the full temporary site hierarchy were verified absent.
The pool task was `01a13023-bba0-71d1-8e7e-5db30a048949`; reservation task was
`01a13023-c428-72de-a20d-44865c4e0063`. Runtime was 17.40 seconds.

Unit fixtures also cover collision/resizing/reparenting rejection, site relocation,
optional-field preservation, updates, identity replacement and convergence across
executor recreation. Adapter/migration race tests passed. This qualification
executes native resource semantics against the appliance; it does not establish
in-cluster journal rollout, dual-stack execution, pruning or full model parity.

### Global authentication executor

Native global authentication-profile updates now use the same operation journal.
Fixture tests passed for PUT list payloads, UUID binding, convergence after
executor recreation, absent/replaced profiles and lost receipts. Full adapter
and migration race suites passed, including ND and existing SWIM coverage.

The live `TestLiveCatalystCenterAuthTemplatesReport` passed in 1.80 seconds after
adding native executor no-op verification: all three built-in global profiles
round-tripped through the resolved model, synthetic drift was detected, and
matching configuration planned no writes. No global authentication policy was
changed. This is not live mutation qualification; fabric-specific behavior and
matching Kubernetes journal rollout remain pending.

### Template project execution

`TestLiveCatalystCenterTemplateProject` passed in 4.22 seconds. It created
`CVK-Q-Project-20261012-125123`, updated its description, verified read-back and
convergence, and verified deletion of fixture UUID
`b9d5474b-4f92-43cd-8e68-89cf46dc3afc`. Create task:
`01a1302c-1ab4-7be1-9320-bc290b01e3a7`; update task:
`01a1302c-1e17-76ef-9f0d-72e5e87eeefa`.

A fixture test exposed escaped query parameters when appended to REST paths.
The Catalyst Center wrapper now passes query values through the existing
foundation REST request field. Adapter/migration race suites passed afterward.
Built-in onboarding project protection and example compilation also have tests.
Template content/version/deployment and Kubernetes journal rollout remain pending.

### Template reference graph

Added and tested the pinned model's canonical project/template identities,
globally unique bare references, same-project disambiguation for composite
members, explicit external references and ordered regular-template dependencies.
Tests reject duplicate identities, missing/repeated members, composite nesting
and invalid qualified references. The offline coverage command exposes an opt-in
reference check while retaining the incomplete-parity exit status and avoiding
configuration-value disclosure. This work is offline model validation; no lab
mutation or template deployment qualification is claimed.

### Template content input qualification

Added pure file-snapshot resolution and an offline `--template-directory` check.
Tests verify project-qualified precedence, shared bare-file fallback, CRLF
normalization, omission of composite body files, missing-file rejection, duplicate
stems, unsupported extensions/path entries, symlink rejection and non-disclosure
of file content. Full parity gating still returns incomplete after successful
content validation. No appliance mutation is part of this offline qualification.

### Atomic template asset source

Added same-ConfigMap asset loading under `cvk-template-assets.json`. The journal
regression test reserves a templates operation, changes only file content and
verifies that submission is rejected as stale while the old in-memory snapshot
is unchanged. A fresh load observes both the new content and new resourceVersion.
JSON tests reject duplicate/non-text/path assets. No new CRDs, RBAC or device
mutations were introduced. Native template content execution remains pending.

### Native template definition reads

Added source-bound definition parsing and redacted draft reporting for regular
and composite templates. Fixture tests cover managed variable fields, device
selector set comparison, composite identity resolution, missing files, replaced
project identities, partial responses and non-disclosure of template bodies.
Adapter/migration race suites passed.

`TestLiveCatalystCenterTemplateRead` passed in 4.79 seconds: 26 draft inventory
entries, three project/UUID-bound detail reads, no values printed and no writes.
Creation/update/version/deployment remain pending; these reads are not evidence
that a committed version exists or that template execution is complete.

### Isolated template commit API qualification

Initial version qualification correctly failed on an unrecognized response:
base-template draft entries omit `version`. The reader now accepts this only
when the entry UUID equals the requested base UUID; absent version fields on
other UUIDs still fail. All 26 pre-existing templates were uncommitted drafts.

A dedicated isolated fixture then passed in 8.19 seconds. Template create task:
`01a1303d-66e8-7a5d-9358-ec460ee39cc7`; commit task:
`01a1303d-6d65-78b9-b832-4a1ac89f7b5d`. Base UUID:
`fd244915-9b69-4e4b-8212-9a1021191045`; committed version 1 UUID:
`7369f779-7889-42c2-827a-a359b82aa4e9`. The committed body matched the intended
harmless template and the project identity matched. Template and project cleanup
were both verified. No device deployment occurred.

Reusable committed-read tests additionally reject wrong UUID/project/name/version
responses. Native manifest-driven draft creation and commit state transitions
remain pending; the live fixture exercises the API contract, not Kubernetes
operation-journal rollout.

### Native regular-template executor qualification

The actual native executor passed create draft → commit → update draft → commit
in 15.88 seconds, recreating the executor between submissions and verification.
Both iterations converged without extra writes. Tasks, in order:
`01a13041-e4e6-78f8-a0fd-6d31dd50c3b0`,
`01a13041-eeea-7243-b620-bec8c20ae80b`,
`01a13041-fb58-73da-a7ef-4489e6f059f0`,
`01a13042-06b3-76df-83b5-69c354a1dab2`.
Template `2f578898-8697-4af7-bbae-97e60479751f` and project
`1b5b0fba-1d16-45d6-a368-3efc479b56ff` were verified removed afterward.
No device deployment occurred. Composite execution and in-cluster journal
qualification remain pending. Adapter/migration race tests passed.

### Native composite dependencies

API qualification confirmed that both draft and committed composites return
member base UUIDs with no member-version field. The native executor therefore
uses verified member bodies plus a UUID-derived dependency marker, while also
verifying the committed composite body independently. Tests prove that a matching
marker alone, or a matching body with a stale marker, cannot complete a commit.

The complete native lab qualification passed in 37.32 seconds. Composite create:
`01a13048-f5bd-7717-b250-45d9736c6106`; initial composite commit:
`01a13049-01d0-7101-a514-d28267818918`; member update/commit:
`01a13049-120f-7dda-9a6e-cd6ee5a9760e` and
`01a13049-1ecc-71d8-a9ce-c3dfaca7dffa`; composite recommit:
`01a13049-31de-7305-b2bc-f521841640f8`.
Composite `3f02793b-473b-4686-9dea-bbafce2966a9`, regular member
`c74197f6-ef6d-43cd-a129-432e0e7f3569`, and project
`5b9baddc-c657-4dbc-8ec4-75b71c715720` were verified removed in dependency order.
No device deployment occurred. Cross-project membership and Kubernetes journal
rollout remain unqualified; full NetAsCode parity remains incomplete.

### Native static tag execution

`TestLiveCatalystCenterTags` passed in 2.81 seconds: create task
`01a1304f-3d61-7ceb-889f-770476393305`, description update task
`01a1304f-41db-7b40-bc9d-9819104b1712`, matching read-back and convergence.
Fixture tag `bed92635-398c-4fce-a248-a0b6d26e7e01` was verified removed. No devices
or templates were assigned. Tests cover dynamic-rule translation and protected-tag
preflight blocking an earlier otherwise-valid mutation. Full adapter/migration
race suites passed. Dynamic rule execution, system tags, inventory tag merging
and membership operations remain pending.

### Native template-to-tag association

The full isolated fixture passed in 26.51 seconds. Tag creation task:
`01a13054-3da2-7dde-85ba-522287146435`; association task:
`01a13054-4615-7e93-8369-f866d5a0cc71`. Membership and convergence were verified
for tag `6f7a0ad5-f79e-4fcd-ad13-bb33148022f5` and template
`93dda698-a04f-4d3e-9ad6-3756ce6e32e6`. Cleanup verified association removal,
tag removal, template removal and project removal in that order.
Fixture tests also prove unrelated members are preserved and replacement tag
UUIDs fail verification. Adapter/migration race tests passed. Device memberships,
protected tags, pruning and in-cluster journal rollout remain pending.

## Inventory identities and tag execution

Read-only configuration identity qualification passed for all three lab switches
(three inventory entries; three corroborated bindings), without printing device
values or performing writes. SWIM identity resolution remains independent.

The inventory tag-only source compiled through the shared tag executor and passed
native create/update/readback/convergence/cleanup in 3.57 seconds:

- Create task: `01a1305d-3085-788d-994e-ab4fd4c0d642`
- Update task: `01a1305d-3510-7ffd-8ef1-c2672dc02c67`
- Fixture tag: `36683484-bae9-44ba-82ac-c6c5e226ab9f` (removal verified)
- Remote log: `/tmp/cvk-catc-model-qualification/inventory-tag-live.log`

Adapter and migration race suites passed. Cross-section ownership fixtures verify
that separate inventory/template owners are rejected for both no-op claims and
prepared writes. This live run exercises native execution, not a rollout of the
new Kubernetes manager/worker/CRD or persistent-journal restart qualification.
Device tag associations, provisioning and combined-section merge are still pending.

## Device tag association qualification

The native device membership executor passed an isolated `.101` lab association,
readback, convergence and cleanup in 6.58 seconds:

- Create tag task: `01a13061-8d45-79b7-a832-d69724f65410`
- Update tag task: `01a13061-91a4-7f3a-bca5-88b954e9aa9b`
- Device association task: `01a13061-976c-7402-abe8-7d169be85b18`
- Fixture tag: `a7fa52c5-0030-435a-87a6-d746bbc1bc8e`
- Remote log: `/tmp/cvk-catc-model-qualification/device-tag-live.log`

Association removal and tag removal were independently verified. No device
configuration or software version was changed. Fixture tests also verify preservation
of foreign memberships, no replay at convergence, and replacement-device/tag
rejection. Manifest site/state dependency integration remains incomplete.

Read-only assignment qualification also passed in 0.55 seconds: three sites read,
three assigned devices, and all three lab identity bindings corroborated. The log
is `/tmp/cvk-catc-model-qualification/assignment-live.log`. Reads require exact
site UUID correspondence; missing or foreign site IDs fail rather than providing
false convergence evidence. Device-changing assignment/provisioning still needs
manifest state planning and coordination with the existing device mutation lease.

## Manifest-driven inventory assignment reporting

`TestLiveCatalystCenterInventoryReport` passed in 0.76 seconds. It constructed a
resolved inventory `ASSIGN` model from the three lab identities and independently
observed the compiled report, confirming zero assignment drift. It performed no
writes and printed no credential, serial or model values. Remote log:
`/tmp/cvk-catc-model-qualification/inventory-report-live.log`.

Fixture tests cover separate assignment/tag drift, convergence, unsupported
states and fields, invalid IP selectors, and the device-apply gate. Adapter and
migration race tests passed; report/apply example compilation and the strict
MkDocs build passed. Role defaults, provisioning/reprovisioning, AP configuration,
and device mutation lease/recovery integration remain incomplete.

## Native assignment executor fixture coverage

Added a one-device assignment resource executor using the pinned provider's
`networkDevices/assignToSite/apply` endpoint. Fixtures verify exact payload and
task receipt, reconstruction without process-local state, authoritative readback,
no replay after convergence, all-target preflight, and rejection of replacement
device/site/serial identities. Explicit serial selectors are required.

Adapter and migration race suites passed, followed by the targeted preflight
assertion test. Strict documentation build passed. This executor is deliberately
not reachable from manifest apply until shared device mutation authorization and
lease recovery are implemented; no live assignment mutation is claimed.

Live no-op assignment planning passed in 2.86 seconds for all three lab devices,
independently confirming their existing assignment. No write was submitted.
Remote log: `/tmp/cvk-catc-model-qualification/assignment-execution-live.log`.

## Durable assignment identity and schema compatibility

Configuration journals now retain the physical serial in addition to device and
site UUIDs. Assignment recovery rejects missing/changed serial bindings. Fixtures
verify that uncertain outcomes retain the serial and cannot release their fence.
A simulated status writer that prunes the serial causes submission to fail before
any remote API invocation. The requested identity is retained independently of
objects modified by the status writer.

The source and Helm CRD copies match. No lab CRD/manager rollout was performed for
this change, and no device assignment write was enabled. Persistent Kubernetes
qualification and device lease authorization remain outstanding.

Final adapter/migration/API race suites, targeted NetworkController manager tests,
and the strict documentation build passed for the durable binding change.

## Final permit readback and managed-device resolver

The adapter/API/migration race suite passed after adding post-permit journal
readback. A storage-pruning fixture verifies zero HTTP calls when identity is
removed during the Submitting write, with the operation fence retained.

Targeted resolver tests passed for canonical serial binding and rejection of
cross-namespace, duplicate physical identities, stale device/node IDs, deleting
devices, address changes and unsupported drivers. This is local prerequisite
coverage; live managed authorization, lease acquisition and assignment mutations
remain unqualified and disabled through manifest apply.

## Live managed device and Node binding

`TestLiveCatalystCenterManagedBindings` passed for all three lab switches. The
initial CiscoDevice projection check passed in 0.22 seconds; the expanded check
including live Node UID and MachineID/SystemUUID passed in 0.20 seconds. Kubernetes
inputs were projected to identity fields only, excluding device credentials.
No writes or lease acquisitions occurred. Remote log:
`/tmp/cvk-catc-model-qualification/managed-binding-live.log`.

The adapter/migration race suite and strict documentation build passed. Fixture
coverage includes missing/conflicting Node inventory and replacement Node/device
UIDs. The manager's existing maintenance contract remains software-upgrade-specific;
configuration maintenance authorization, acknowledgement and lease recovery are
still required before assignment writes are enabled.

## Combined inventory/template tag reporting

Combined reports now honor NetAsCode's inventory-over-template definition
precedence, count each effective tag once and retain unique template tags. Tests
cover section-order independence and malformed shadowed declarations. Adapter and
migration race suites, report example compilation and strict MkDocs build passed.

Live isolated qualification passed in 3.51 seconds:

- Create task: `01a1307b-04be-7f02-8dba-e864205c7a10`
- Update task: `01a1307b-0917-70c2-98bf-e3dcc2851b28`
- Fixture tag: `2ceb6046-4e53-471c-a694-265330215778` (removal verified)
- Remote log: `/tmp/cvk-catc-model-qualification/tag-merge-live.log`

A conflicting template description was correctly shadowed by the inventory
value, producing no false drift. Combined apply remains unsupported. This work
does not enable device assignment writes or change existing SWIM admission.

## Combined template/inventory apply

The combined apply compiler now merges definitions with inventory precedence and
uses one canonical executor/journal namespace. Reconciler fixtures passed across
worker reconstruction with one create, both section claims, no double-counted
status, and convergence. Ownership-conflict tests verify no partial claim. Tests
also verify safe old-section boundaries are released while accepted work stays
fenced when a config transitions to combined sections.

Live native combined-source create/update/readback/report/cleanup passed in 3.75s:

- Create task: `01a13080-fc71-7deb-ba0c-b2d081b8c300`
- Update task: `01a13081-00ec-7bff-b0de-c1ae519e3ea3`
- Fixture tag: `31a71ea4-fa6b-44e0-9850-365e9915f525` (removal verified)
- Remote log: `/tmp/cvk-catc-model-qualification/combined-apply-live.log`

Adapter/migration race tests, example compilation and strict documentation build
passed. Live testing exercised native execution; the new Kubernetes journal/CRD
rollout is still pending. Inventory device actions remain rejected by combined
apply until their mutation authorization and recovery are complete.

## In-cluster journal rollout and restart qualification

The matching configuration-journal schema was added to the lab CRD, and the
manager/controller worker were rolled to `cvk-catc-nac:20261012-0146d7a983a4`.
The binary SHA-256 is
`0146d7a983a4e59dd8ec158c7bb9b36c31bfea1cfd9eaf9005fadc69d19c6176`.
Device-worker image flags and deployments were preserved. Before rollout, the
three disruptive-mutation leases were idle; two historical nonterminal upgrade
phases had manager admission marked Settled.

The uniquely named fixture was first reported absent, then applied through a real
NetworkControllerConfig and worker. After Accepted was persisted, the worker Pod
was deleted and replaced. The same journal operation and task receipt progressed
through Verifying/Verified to InSync. One task was observed for this generation:
`01a1308d-0bd5-72f5-a674-6dfeb4fd1314`.

The next manifest generation updated the tag description. The manager was restarted
after the update receipt was stored. The journal settled and both ownership claims
survived. The update task was `01a1308f-6c03-7004-9aec-b24c1d5e1b7b`.

Credential-free evidence:

- [Create and worker restart](journal-create-restart.json)
- [Update and manager restart](journal-update-restart.json)
- [Post-rollout health](journal-rollout-health.json)

Both controller deployments completed rollout. The three UID-bound lab nodes
remain Ready, and their app/network workers retain the October 9 image. An initial
prefix-based health check also selected the unrelated `cat9k-node` orphan, whose
last heartbeat was in January. It was left untouched; the final check uses declared
CiscoDevice-to-Node bindings.

The config `cvk-live/catc-nac-journal-qualification` (UID
`5c9724f7-af1a-4fe2-8b9c-724f996f2470`) remains InSync at generation 3. Its tag
`CVK-NAC-Journal-0146d7a983a4` and both section claims are intentionally retained as
the baseline for continued qualification. Do not create a competing template or
inventory owner. Rollback snapshots and exact lab scripts are retained under
`/tmp/cvk-nac-journal-rollout` on `vk-ubuntu16`.

This proves real journal persistence and convergence across the exercised restarts.
It does not prove every possible crash boundary, uncertain-request recovery,
pruning/ownership transfer, device mutation authorization, or full NetAsCode parity.

## Switching network profiles and site assignments

The native profile qualification passed on the lab appliance in **37.29s**.
`TestLiveCatalystCenterTemplateProject` reused isolated project/template fixtures
with `CATC_NETWORK_PROFILE_EXECUTION=isolated-profile`. It exercised the native
configuration compiler/executors and reconstructed the executor between each
submission and verification. This is resource-level qualification; this new
profile code has not yet been rolled into the running Kubernetes worker image.

Verified sequence:

- Empty switching profile create, task `01a130aa-d4b0-7ae4-85d5-901791762bea`.
- Add a committed regular template by project-qualified reference/base UUID,
  task `01a130aa-dd0b-7b13-9669-ed8e9f255a6d`.
- Create an isolated empty area, task `01a130aa-e668-7bd1-996a-b08d919d3930`.
- Assign that area, task `01a130aa-ee0b-7cb3-94af-b9a84e4938f6`.
- Remove and restore the profile's template association after site assignment,
  tasks `01a130aa-f7fd-745c-ba7d-fa2a0c2b4081` and
  `01a130ab-021c-7213-8b12-5f68d4d6c55d`.
- Verify convergence and zero report drift, retaining the site assignment.
- Unassign the isolated site and delete the profile and area with successful
  task completion and absence checks; then verify template and project cleanup.

Fixture IDs (all verified removed):

- Profile `c8adddc0-80c7-47e3-bde3-1123efb2b906`.
- Area `d1ffa634-ec63-44f0-b9ea-f83f3936719a`.
- Template `c0ed674d-950a-4005-a013-fabedfcb0a88`.
- Project `9c666f6f-3aa2-438f-aeb8-d6abb8cef8f8`.

The lab runs identified and corrected three API-contract details before the
passing run: profile readback requires `populated=true`; the single-site endpoint
accepts an `id` object (the upstream `items` payload uses `/bulk`); and site
assignment materializes a server-owned `site.settings` subtree. The latter is
preserved opaquely in memory during template updates and excluded from drift,
status and dependency hashes. Earlier runs retained their fixtures on mismatch
or rejected submission; task/readback inspection preceded UUID-bound cleanup.
All three earlier fixture sets were verified removed before subsequent runs.
No template was deployed to a device and no existing device site was modified.

Contract/race tests cover duplicate identities, ambiguous bare template names,
resolved aliases, missing later dependencies, metadata-only responses, namespace
isolation, uncertain receipts, executor reconstruction, replaced profile/site/
template UUIDs, inherited-settings preservation, and retention of unrelated
site associations. Adapter/migration race regressions and strict documentation
build passed. Onboarding/composite profile references, managed-site scope,
cross-section dependency creation, pruning and live Kubernetes profile-journal
restart qualification remain outstanding; this does not establish full model
parity.

## System external authentication and synchronous journal responses

Added native compilation/reporting and update execution for the pinned model's
`system_settings.external_authentication.enabled` and `aaa_attribute`. Both use
validated synchronous HTTP 200 responses and independent readback. The shared
journal persists the existing submission permit, records successful synchronous
responses as `Verifying`, and never invents a task ID. Asynchronous resource
executors retain their existing task receipts and polling behavior. No public
journal fields or other infrastructure-provider structures changed.

The lab currently has **zero authentication-policy servers**. Its external
authentication flag is false and its custom AAA attribute is empty. Both POST
endpoints returned HTTP 400 when tested without a registered server. The custom
attribute error explicitly required registration of at least one AAA server;
subsequent readback confirmed the empty attribute and disabled baseline. These
rejections do not qualify mutation success. The planner now rejects either
mutation before preparing a journal operation if no policy server is observed.

Final live results:

- `TestLiveCatalystCenterExternalAuthenticationReadOnly`: **PASS**, 0.16s;
  native API reads, report convergence and no-op executor planning verified.
- `TestLiveCatalystCenterExternalAuthenticationAttribute`: **SKIP**, 0.16s;
  prerequisite absent. This is explicitly not a successful mutation test.
- No custom attribute or authentication-policy fixture remains from these tests.
  Authentication remained disabled throughout.

The default `vk-ubuntu16` jump route failed authentication during transfer.
Access was restored through the already authorized `svr-xl` route, verifying
that the target was still `ubuntu16`; no SSH configuration or credentials were
changed. Current route for continuing lab qualification:
`ProxyCommand=ssh -o BatchMode=yes -o ConnectTimeout=10 -W %h:%p svr-xl`.

Local race tests verify reconstructed-reconciler convergence, exactly one
submission per setting, no task polling for synchronous responses, malformed/
missing/202/task-shaped response rejection, authentication-error no-replay,
missing-server preflight, and both lost and persisted-but-unacknowledged
completion receipts. Unknown outcomes remain fenced. Adapter/migration
regressions, example compilation, strict documentation and diff whitespace
checks passed. Policy-server/ISE creation, secret-reference injection, live
setting mutations, and deployment of this new code to the Kubernetes worker
remain pending. Full system-settings parity is not claimed.

API contracts used:

- [Manage external authentication](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/manage-external-authentication-setting-api/).
- [Add/update custom AAA attribute](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/add-and-update-aaa-attribute-api/).
- Pinned `CiscoDevNet/catalystcenter` v0.6.1 resource/model sources for
  `external_authentication`, `external_authentication_aaa_attribute`, and
  `authentication_policy_server`.

## Temporary AAA registration unlocks external-authentication qualification

Native non-ISE policy-server registration and metadata readback are now
implemented internally, together with pinned-model AAA metadata reporting.
The server inventory uses `instanceUuid`, correcting the earlier prerequisite
reader's assumption of `id`. Credentials are excluded from the observed type.

A temporary RADIUS registration pointed to the authorized lab runner at
`192.0.2.43` with external authentication kept disabled. A random shared secret
was generated in process memory, sent only to the lab controller, and never
written to test output, repository files or Kubernetes status. This fixture is
not a working RADIUS service and was never used to enable external login.

Final live qualification passed in **9.30s**:

- Create receipt: `01a130c8-56c2-73d4-9298-3a206283c1e3`.
- Observed server UUID: `176ae8b1-6b77-4a10-932b-fa5a0e4e992b`.
- Verify task completion, server UUID, address, protocol, role, ports, retry and
  timeout metadata.
- Compile/report the pinned NetAsCode AAA metadata model together with the
  external-authentication flag; verify zero drift.
- Validate a synchronous request retaining the disabled flag and its readback.
- Execute two custom AAA attribute changes, reconstruct the executor before
  verification, and verify convergence and zero drift (subtest **6.12s**).
- Restore the empty attribute and disabled flag, then delete the temporary
  server and independently verify the empty policy-server baseline.

An earlier native-API-only run also passed (**10.08s**) and removed server
`046f34ab-e488-4718-a6ad-7c2c5054891c`, created by task
`01a130c6-a335-7e7d-8f3d-29ce3b03b750`. The second run added actual pinned-model
report verification. All fixture servers were removed. SSH authentication was
intermittent; failed launches ended before remote execution, and retry followed
that evidence. The temporary local SSH multiplex socket did not persist, so the
final run used one explicit `svr-xl` connection for transfer and execution.

Local race regressions cover registration refusal for existing addresses,
missing receipts/no replay, UUID replacement, duplicate/incomplete inventories,
credential-free projections, known/unknown model fields, integer validation,
masked-secret rejection, and the continued manifest-write gate. Adapter/ND and
migration regressions passed, as did report-example and strict documentation
checks.

This closes native registration and external-setting API qualification gaps,
not full authentication-policy-server parity. Manifest-driven server creation,
existing-server updates, credential snapshot/version binding, secret rotation,
ISE integration, key wrapping, real RADIUS/TACACS authentication, external-login
enablement and Kubernetes journal qualification for these new resources remain
pending. The running controller worker image has not been updated with this
turn's code.
## Projected credential snapshot qualification

The adapter-local resolver was qualified with filesystem and journal tests.
The resolver reuses the existing per-key projection contract, rejects escaped
mount paths and unauthorized destinations, and preserves credential bytes only
in memory. Tests model Kubernetes symlink rotation, material rotation, alias
revocation/remapping, config/controller replacement, malformed pointers and
inline-value replacement attempts.

Journal tests rotate the material after `Prepared` and after `Submitting`.
Both cases prevent the API callback. The latter retains `Submitting`; a newly
constructed snapshot cannot replay the uncertain operation. Missing or already
changed snapshots cannot prepare an operation. Journal serialization contains
no credential values.

Validation: `go test -race ./internal/controlleradapter/...
./tools/cvk-netascode-migrate` passed, including Catalyst Center and Nexus
Dashboard. This qualification is local. No appliance writes or worker image
rollout were performed for this resolver change. Public manifest credential
execution remains gated pending durable write-only version receipts.
## Write-only version journal qualification

Internal operation bindings and atomic applied-version receipts were added.
Targeted tests cover changed counters, changed source references, duplicate
paths, absent snapshots, missing expected bindings and field pruning at both
Prepared and Submitting. Generic metadata verification cannot release a
credential operation. Completion tests require a successful correlated task
and matching existing resource UUID, reject uncertain or incomplete outcomes,
and verify atomic receipt persistence after a lost acknowledgement. A pruned
applied receipt prevents journal-slot release. Receipts retain version counters
and references only, and survive release of a verified operation.

The controller adapter and migration race suites passed, including ND. API
deep-copy code and both NetworkController CRD copies were regenerated. This is
local qualification of internal mechanisms, not a manifest AAA execution test
or appliance rollout. Compiler wiring, rotation planning, receipt capacity
preflight and live qualification remain outstanding.
## AAA manifest execution and live rotation qualification

`TestPolicyServerManifestCreateRotateAndRestart` passed. It rebuilds the worker
reconciler between steps, creates the server, persists its application receipt,
increments the explicit credential version and changes the projected material,
updates exactly once, and converges without subsequent writes. Tests also
reject foreign/replaced server UUIDs, counter rollback, unversioned source
changes, keywrap overwrites and receipt-capacity exhaustion before submission.
The controller adapter and migration race suites passed, including ND.

The Linux live test then ran on Ubuntu16 through svr-xl against Catalyst Center:

- Create task: `01a130df-9253-7d84-a928-c762ded3328e`.
- Verified server UUID: `e324e6af-babb-4d1e-9457-5a1665ead5b9`.
- Version-driven PUT task: `01a130df-98cc-716b-abae-6dc4ba0dc700`.
- Same-UUID metadata verification and subsequent no-op planning passed.
- Disabled-flag request and custom-attribute changes/restoration passed.
- Server deletion completed and the zero-server baseline was verified.
- Overall test passed in 12.02 seconds; custom-attribute subtest in 6.22 seconds.

The API executor used in-memory fixture version bindings. This is not proof of
a real Kubernetes worker rollout, live projected Secret rotation or external
RADIUS authentication. Those distinctions remain explicit in the parity guide.
The remote log is `/tmp/cvk-catc-model-qualification/policy-live.log`; no
credential values were printed. No manager/worker image or CRD was deployed in
this qualification.
## Pre-rollout projection-lag correction

Preflight confirmed the manager/controller worker still used
`cvk-catc-nac:20261012-0146d7a983a4`, the three device mutation leases were empty,
and the historical nonterminal upgrade objects had Settled manager admission.
The candidate image `cvk-catc-nac:20261012-9e39631ede8d` was built/imported and
the CRD/deployment changes passed server-side dry run. **It was not deployed.**

Review identified that a separate manifest counter and bare projected value
could associate stale credential bytes with a newer version during kubelet
projection lag. The adapter now requires one atomic projected JSON key carrying
the matching `version` and `value`. Strict parsing rejects bare strings,
duplicate/unknown fields and mismatched versions without exposing values.
The journal's version validation checks this binding before submission.

The full manifest test now advances the desired version while deliberately
retaining the old projection. Repeated reconciliation produces no API write or
new operation until the matching projection arrives. The adapter/migration race
suite passed after this correction, including the Secret-bearing example's
actual compiler path. The previously imported candidate is superseded and must
not be used for rollout; a corrected binary and live qualification are pending.
## Live AAA manifest rollout, restart and rotation

The corrected manager/controller-worker image
`cvk-catc-nac:20261012-43741637f05c` and credential-receipt CRD schema were
installed. Binary SHA-256:
`43741637f05c72437d90fdde735646e785c4755fdd91ea1715ae05caac9740dc`.
An initial health read preceded the manager's worker-image reconciliation;
the subsequent read confirmed both deployments updated and Ready. Existing
XE app/network worker images and the NX-OS worker image were preserved.

Real `NetworkControllerConfig` `cvk-live/catc-aaa-qualification`, UID
`4510b61d-d0e3-41a9-a7b5-72f104d51c4d`, used an administrator-authorized
projected Secret key with an atomic version/value wrapper.

- Create task: `01a130ea-d4ae-78a1-bc62-58098bc01caa`.
- The worker Pod was deleted after Accepted; a replacement completed the same
  journal operation and reached InSync with its durable version-1 receipt.
- Server UUID: `d835b7c8-53eb-4b63-a2cb-5e03697655e3`.
- The model counter was advanced to 2 while the projection remained at 1.
  Repeated checks over 12 seconds found no new operation and the old applied
  receipt unchanged. After the version-2 wrapper arrived, rotation proceeded.
- Rotation task: `01a130ec-1539-74a1-a726-d97413db7eaa`; the same UUID reached
  InSync with a durable version-2 receipt.
- Cleanup first changed the config to report mode and confirmed its observed
  generation with an empty operation slot, then deleted the exact server UUID.
  Delete task: `01a130ec-7560-7539-965b-ff553248c276`.
- The zero-server baseline was verified. Removing the alias initially failed;
  a fresh conditional update removing the now-empty optional field succeeded.
  The temporary Secret was deleted. No controller operation was repeated.

The report-only config, ownership and non-secret application receipt are
retained for reuse. Future system-settings tests must use this config identity
or an explicit ownership lifecycle. The existing inventory/templates config
remains InSync, all three bound XE nodes are Ready, and the final journal is
empty. This does not qualify external RADIUS authentication, login enablement,
ISE or key wrapping.

Sanitized evidence: [aaa-manifest-qualification.json](aaa-manifest-qualification.json).
Remote operational artifacts are under `/tmp/cvk-aaa-rollout/`.
## Combined system-settings execution qualification

The compiler now accepts non-ISE AAA servers and external-authentication
settings together. A dependency wrapper reuses the existing server and singleton
executors and the single journal. It observes all selected surfaces before
planning a write, requires server convergence and a persisted credential
receipt before dependent settings, and routes asynchronous credential proof
separately from synchronous singleton verification.

`TestCombinedSystemSettingsManifestCreateRotateAndRestart` passed: reconstructed
reconcilers created the server, recorded its receipt, changed the external flag
and custom attribute in order, converged, then rotated the credential without
repeating external writes. Preflight read failures and unknown intent are
rejected, and missing credential fields cannot redirect a server operation
into singleton verification. Combined example compilation and the full adapter/
migration race suite passed, including ND.

This is local qualification. The installed `43741637f05c` build predates this
combined executor; no live enablement or combined-manifest success is claimed.
## Live combined-system-settings qualification

Manager/controller worker were rolled to
`cvk-catc-nac:20261012-c3d4574c3167`, binary SHA-256
`c3d4574c31677b4c06538baf9e1c11aa0c1baa9f636d147a98edb10220d5ffcf`.
The retained qualification config UID was reused with atomic projected
credential version 3, disabled external authentication and a custom AAA
attribute. Existing device-worker images were preserved.

- Server task: `01a130f6-9122-7d58-ae9b-1e343a0771f5`.
- Server UUID: `fb3d9df6-d0c8-4244-8c70-16b2bcfec95b`.
- The persisted server receipt was present before custom-attribute operation
  `aca2b66d18849969f62c718b3de24a5c` began.
- The worker Pod was deleted after this synchronous operation reached
  Verifying with an empty task ID. The replacement completed the same journal
  entry, and the combined config reached InSync. API reads confirmed the custom
  attribute and disabled login. One asynchronous server task was recorded.

An SSH disconnect interrupted delivery of the final output. Read-only inspection
confirmed the remote process had stopped, convergence was durably recorded and
cleanup was at its report-only boundary. The first cleanup helper incorrectly
attempted an empty-attribute POST, which returned HTTP 400 without restoring the
attribute. Inspection of the existing live test/provider confirmed the required
DELETE reset. The corrected helper restored the empty attribute and deleted the
server through task `01a1314a-a4e1-73ab-b314-2d2bd5894198`; the zero-server
baseline was verified and the temporary Secret/alias removed. No completed
manifest operation was replayed.

Final health confirmed the three bound XE nodes Ready, matching manager/worker
images, the existing inventory/templates config InSync and no pending journal
operation. The qualification config remains report-only with its owner and
version-3 receipt retained. External login enablement and actual RADIUS
authentication were not tested.

Evidence: [combined-system-settings-qualification.json](combined-system-settings-qualification.json).
Remote artifacts: `/tmp/cvk-combined-rollout/`.


### Key-wrapping API qualification: compatibility failure

See [sanitized evidence](keywrap-api-qualification.json). The current source's
isolated AAA executor successfully created a fixture and rotated its shared
secret. The following KEYWRAP update task completed without an error, but the
appliance returned an empty encryption scheme. Verification refused convergence;
independent encryption-key rotation was not reached. The fixture was inspected
without replay and removed by its verified UUID after the task had settled.
The empty-server baseline was restored with external authentication disabled.
The lab worker image was not changed. Key wrapping is not live-qualified.

Local coverage includes independently versioned key projections, full-object
payload preservation, stale projections, duplicate bindings, counter rollback,
removal refusal and the appliance's ignored-scheme response. The existing
controller/ND/migration regression race suite passed; the latter response has a
focused regression test. No credentials or credential hashes are recorded here.


### Global device credentials: six native lifecycles qualified

The [sanitized task evidence](global-credential-qualification.json) records
isolated CLI, HTTPS read/write, SNMPv2 read/write and SNMPv3 AUTHPRIV definitions.
Each native executor created one uniquely named unassigned definition, verified
its task and UUID/metadata, refused a stale projection, rotated one credential
counter, and converged without replay. Cleanup deleted only that UUID and checked
that the original inventory counts and IDs remained. No site assignments or
actual device authentication were attempted; the lab worker image was unchanged.

Full manifest fixture tests separately exercise each of the six types through
projected files, task/credential receipts, worker reconstruction, delayed
projection and no-op rotation. These tests do not establish deployed-worker
qualification. SNMPv3 NOAUTHNOPRIV still needs a credential-independent ownership
receipt; AUTHNOPRIV has parser/fixture support but was not live-qualified here.


### Global credential manifests: deployed worker qualification

The [worker evidence](global-credential-worker-qualification.json) records image
`cvk-catc-nac:20261012-312cf520517e`, six creates and six independent credential
rotations. A worker pod was deleted after the first create's task was Accepted;
the replacement completed that same operation without replay. All six
registrations reached InSync with durable UUID/version receipts.

Advancing one counter per definition while retaining old projections produced
`ObservationOrPlanFailed` for the current generation, with no submitted operation
and unchanged receipts. Matching atomic projections then completed all six
rotations, preserving the other counters. The configuration was changed to
metadata-only report mode and reached InSync before targeted deletion of the six
fixture UUIDs. Original credential inventory IDs/counts were restored, and the
temporary Secret and aliases were removed. Final worker/node health passed.

The retained network_settings owner is
`cvk-live/catc-global-credential-qualification`, UID
`3ca01713-4714-48ad-9fe2-b7ad5c39b305`. It remains report-only with applied receipts;
its removed fixtures can now appear as Add drift. Reuse this owner for subsequent
network-settings qualification until ownership transfer is implemented. Do not
create a competing global owner or delete it to manufacture ownership evidence.
ISE integration, site assignment, actual device authentication, SNMPv3
NOAUTHNOPRIV ownership and whole-section parity remain outside this qualification.

- [Site credential inheritance investigation](site-credential-inheritance-qualification.json): nested null disabled the slot; top-level null failed with NCND00010. Removal is gated before execution; temporary resources were removed.

- [Site credential assignment qualification](site-credential-assignment-qualification.json): all six named assignment types passed native task/readback/no-op checks on an empty area. Temporary credentials and the area were removed. Worker deployment and device authentication are not claimed.

- [Combined network credential worker rollout](network-credential-worker-rollout.json): combined definitions/settings/assignments passed fixture tests and the new worker rolled out healthy. Direct-device worker images and Ready node identities were preserved. This is not yet live combined-manifest qualification.

- [Combined network credential worker qualification](network-credential-worker-qualification.json): all six credential definitions, one NTP setting, and all six assignments converged through the deployed journal; ordering, assignment-task restart recovery, unchanged-intent reconciliation and cleanup verified. Twelve task IDs were captured; the first CLI create was already complete when observation began and is evidenced by its persisted receipt and UUID readback.

- [Discovery qualification](discovery-qualification.json): native single-address discovery on existing .101 passed task/job correlation, completion/no-op, unchanged device identity and job cleanup. Worker execution remains pending; device outcome reads are qualified separately below.

- [Discovery outcome qualification](discovery-outcomes-qualification.json): read-only native enumeration of the existing Switches job verified all three expected devices with successful CLI/SNMP/NETCONF and Managed/Reachable collection. This is historical job evidence, not a fresh authentication run or worker rollout.

- [Combined discovery no-op qualification](discovery-combined-noop-qualification.json): existing lab tag and discovery converge without mutation in either section order. Combined create/restart is fixture-tested; worker qualification is pending.

- [Device role no-op qualification](device-role-noop-qualification.json): all three lab switches reported ACCESS/AUTO; physically bound native planning returned no writes. Role mutation, AUTO recalculation and manifest admission remain pending.

- [Discovery worker qualification](discovery-worker-qualification.json): combined manifest created a SINGLE/SSH job for existing .101, resumed the same accepted task across worker replacement, verified job/device identity, and published eventual Managed/Reachable with successful CLI/SNMP. NETCONF remained unverified (not requested). Original intent/discovery inventory restored; all three Cisco nodes Ready and direct-device images unchanged.

- [Global credential assignment read](global-credential-assignment-read.json): six root slots read successfully, all null. No existing named assignment was available for live no-op validation; root writes and restoration remain unqualified.

- [Fabric L3 qualification attempt](fabric-l3-qualification.json): not qualified. Malformed fixture name was rejected; a corrected name reached a task but failed during business-intent preprocessing with NCSP11000. Inventory remained the two built-in VNs; no cleanup was required.

- [Task failure read qualification](task-failure-read-qualification.json): existing NCSP11000 task is classified as a sanitized configuration failure. No operation was resubmitted. Failed-phase status and retained-journal restart behavior are fixture-tested.

- [IP transit qualification attempt](fabric-transit-qualification.json): task failed with NCSP11008, reporting a missing ConnectivityDomain application package. Inventory remained empty. No retry or cleanup mutation was made; live qualification remains pending.

- [Wireless interface qualification](wireless-interface-qualification.json): native create, VLAN update with retained UUID, read-back/no-op and cleanup passed. Baseline restored. Manifest restart is fixture-tested; deployed-worker qualification remains pending.

- [Wireless ACL qualification](wireless-acl-qualification.json): native IPv4/TCP create, port update, UUID read-back/no-op and cleanup passed with explicit `1-65535` source ports. Appliance rejects upstream `0-65535`; baseline restored. IPv6, URL changes and worker qualification remain pending.

- [Wireless power-profile qualification](wireless-power-qualification.json): native Ethernet speed rule create/update, UUID read-back/no-op and cleanup passed; baseline restored. Other rule combinations, AP application and worker qualification remain pending.

- [802.11be qualification](wireless-dot11be-qualification.json): native create, Multi-RU update, UUID read-back/no-op and cleanup passed. Baseline restored. Inventory contained no WLC; provisioning and radio behavior remain unqualified.

- [RRM FRA qualification](wireless-fra-qualification.json): task-based create, interval update, UUID read-back/no-op and cleanup passed. Both protected system designs preserved; receipt-contract probe also removed. Worker and WLC qualification remain pending.

- [CleanAir qualification](wireless-cleanair-qualification.json): native 2.4 GHz create/enable update, UUID read-back/no-op and cleanup passed; original designs preserved. Other bands, individual interferer changes, worker execution and radio behavior remain pending.

- [Combined wireless worker qualification](wireless-worker-qualification.json): interface, pre-auth ACL, power and 802.11be create/update/report/cleanup passed with accepted-task worker restart. FRA and CleanAir reached the same result, but FRA creation required supervised restoration of its successful task receipt before restart/read-back. All fixtures removed and baseline restored; XE and NXOS workers healthy. This run does not qualify unattended FRA receipt recovery or the later conflict-retry fix.

- [Receipt conflict fix rollout](receipt-conflict-rollout.json): regression-tested bounded status-only retries deployed to manager and Catalyst Center worker. Journal idle, retained discovery configuration InSync, all three Cisco nodes Ready, direct-device images unchanged. A fresh manifest qualification on this image remains pending.

- [Anchor-group qualification](wireless-anchor-qualification.json): native external-anchor create, priority update, UUID read-back/no-op and cleanup passed. Baseline restored. Managed-WLC resolution, worker deployment, SSID associations and mobility behavior remain unqualified.

- [AP profile metadata read](wireless-ap-metadata-read.json): two built-in profiles read through the public metadata allowlist. Observation-only sentinel normalization verified; no mutations or AP execution qualification claimed.

- [AP profile native execution](wireless-ap-execution-qualification.json): isolated create/client-limit update/readback/no-op/cleanup passed and baseline restored. Native rotation boundaries are fixture-tested; manifest and worker integration remain pending.

- [AP manifest worker rollout](ap-manifest-worker-rollout.json): new manager/controller worker healthy before qualification; direct-device images and Ready node identities preserved.

- [AP manifest worker qualification](ap-manifest-worker-qualification.json): combined power/anchor/AP creation, accepted AP-task worker restart, three versioned passwords, stale-projection refusal, rotation to version 2 and client-limit update completed. Original report intent, public inventory and healthy workers restored; temporary definitions, Secret and sources removed. Harness aliases and condition handling needed correction/resume; no controller request was replayed or journal manually repaired. Device authentication and calendar changes remain unqualified.

### RF profile definitions

`wireless-rf-qualification.json` records native executor qualification of an isolated, unassociated custom RF profile: create, update of the 5 GHz maximum client count from 100 to 200, task completion, readback, no-op, deletion and baseline restoration. All three built-in profiles remained unchanged. The initial attempt stopped before mutation because built-in 6 GHz parent values differ from the pinned write schema; a captured fixture now tests that observation-only distinction.

The branch maps all 88 pinned RF fields and includes shared-journal restart tests, UUID checks, explicit default deselection, built-in protection and refusal to update profiles containing unmapped API fields. This evidence does not qualify all field combinations, manifest-worker deployment, default transitions, physical AP/WLC behavior, adoption or prune.

RF follow-up validation adds checks against retained controller bounds and mandatory data-rate subsets, plus canonical channel/data-rate set comparison. These changes have local regression coverage. After transient SSH authentication failures, the latest binary was restaged and deployed as `cvk-catc-nac:20261013-d5520c90d6d0`.

[RF manifest-worker qualification](rf-manifest-worker-qualification.json) records two isolated profiles created through the shared journal, a worker restart after task acceptance, verified default selection/transfer/clear, and maximum-client changes from 100 to 200. The same task and operation continued across the restart. Original report-only intent and the complete built-in RF inventory were restored, and both fixtures were deleted. Final checks show the manager/controller worker at the new image, unchanged XE/NXOS images, three Ready switches and an idle journal. No journal repair or controller-request replay was required. Other RF field combinations, physical AP/WLC behavior, association ownership, adoption and prune remain unqualified.

### SSID inventory groundwork

[SSID inventory read](wireless-ssid-inventory-read.json) verifies native Global-site resolution and authenticated reading of an empty SSID collection. No controller configuration was submitted. Populated metadata, sentinels and passphrase exclusion are fixture-tested; this read-only evidence does not qualify SSID execution, rotation or wireless provisioning.

[SSID native progress](wireless-ssid-native-progress.json) records a successful disabled OPEN definition create/readback/no-op followed by a preflight refusal before update. Extra appliance response fields now have guarded compatibility handling and regression tests. SSH authentication prevented uploading the fix for resume; the named disabled fixture remains. Update, deletion and worker credential qualification are pending. Local manifest reconciliation tests now cover durable credential versions, restart, stale projections, rotation, removal refusal and schema-pruning fences.

SSID native follow-up resumed the same fixture after two preflight-only refusals. Update to 240 seconds, authoritative readback, no-op, deletion and empty baseline restoration passed. Enabled unsupported extension fields remain fenced. The local combined ACL/SSID test verifies dependency ordering and preflight refusal before prerequisite submission.

The subsequent native WPA2 credential qualification was attempted twice; both attempts timed out at initial Global-site resolution before any mutation. An independent authenticated-client diagnostic also timed out. No credential fixture was created. The next worker binary was built locally (`cvk-catc-nac:20261013-e7ccf15912a3`), but neither its image nor the additive credential-key CRD has been deployed.

The next credential attempt returned HTTP 401 Unauthorized during initial token acquisition. Authentication retries stopped and the user was asked to verify the lab account or refresh `cvk-live/catc-credentials`. No SSID was created. Local multi-PSK manifest reorder/rotation tests and wireless profile/tag model work continued independently.
