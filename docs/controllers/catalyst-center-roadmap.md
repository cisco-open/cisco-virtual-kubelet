# Catalyst Center roadmap to full parity

Baseline: NetAsCode **0.5.0**, immutable module revision
`a435555fe828f03cf3311d52fe1ab6572a247916`; **11 sections, 93 resource types**.
Status date: **19 October 2026**. This is the remaining work after the
[alpha checkpoint](catalyst-center-alpha.md), not a claim that the existing
bounded implementations must be rebuilt.

The [resource ledger](catalyst-center-resource-parity.md) remains the detailed
backlog. Every row must link its implementation and evidence before closure.
“Implemented”, “temporary API qualified”, “native API qualified” and “installed
physical-device qualified” are distinct states. Unavailable lab equipment is an
open gate, not a pass. No whole section is complete at this checkpoint.

## Priority and dependencies

| ID | Priority | Remaining deliverable | Completion evidence |
| --- | --- | --- | --- |
| CC-01 | P0: merge safety | Review shared admission, reservation, handoff and workload changes with the ND dependency; validate the actual PR head and destination | Required CI green; matching CRD/chart/runtime review; ND and direct-device regression results; explicit stacked merge decision |
| CC-02 | P1: active switching lifecycle | Publish versioned latest applied-effect receipts bound to original task, owner, source, native UUID, comparison key and predecessor | Atomic receipt replacement before journal release; capacity preflight; conflict/lost-response/restart tests and native qualification |
| CC-03 | P1: private state history | Complete comparison-key history/rotation and handling of old creator-only records | Missing/changed key refuses; no retroactive fingerprint from arbitrary current state; rotation and recovery evidence without secret disclosure |
| CC-04 | P1: acquisition recovery | Finish partial PnP acquisition cancellation with proof that the original manager is quiescent | Actor-separated installed cancellation/restart tests; no release while original execution may continue |
| CC-05 | P1: ownership lifecycle | Close resource-specific adoption, retained-source omission, prune, config/controller deletion, handoff, retirement and bounded-ledger exhaustion | Native identity and consumer checks; preserved receipts/tombstones; recreation and uncertain-outcome tests; no adoption by name alone |
| CC-06 | P2: model semantics | Close YAML/schema/default/filter/inheritance and catalogue gaps for every pinned resource | Independent pinned upstream fixtures; unsupported/duplicate/malformed inputs rejected; deterministic effective source and private provenance |
| CC-07 | P2: remaining operations | Finish each resource's missing read/plan/apply/update/prune and dependency transitions | Public manifest reconciliation plus original-task recovery and independent readback; resource ledger updated with exact limits |
| CC-08 | P2: installed qualification | Exercise matching manager, worker, admission and status persistence in disposable installed clusters | Pod/manager restarts, source/projection changes, actor denial, cancellations, deletion and recovery; recorded versions and cleanup |
| CC-09 | P2: physical and scale qualification | Exercise supported real-device workflows, fan-out, pagination, rate limits and partial failure | Representative hardware/controller versions, device postconditions, reservation exclusivity with SWIM/gNOI, bounded concurrency and exact cleanup |
| CC-10 | P3: parity closure | Close all 93 resource rows and promote only evidenced section capabilities | Reviewed resource-to-operation evidence matrix; no unresolved required schema/lifecycle gates; reproducible install/upgrade runbook |

P0 items block an alpha merge. P1–P3 items may remain open only within the alpha
operating contract: unsupported operations stay refused, and bounded operations
keep their existing qualification limits. If qualification finds a safety defect
in an enabled path, fixing or disabling that path becomes P0.

CC-02 depends on the complete private switching observations already implemented;
do not repeat that prerequisite. CC-03 and CC-05 build on its durable receipts.
CC-04 proceeds within the existing shared reservation protocol. CC-06/CC-07 can
advance in independent resource slices; each slice needs CC-08/CC-09 evidence
before CC-10. This ordering does not introduce another transport, secret store,
upgrade executor or mutation-lock domain.

CC-01 also retains the restored-manager startup diagnostics from the earlier
SWIM review. That intermittent probe failure had no proven root cause despite
later passing exact-head CI. Repeat the positive startup/restart checks on the
new head and destination, and investigate a recurrence rather than treating a
historical pass as its resolution.

## Remaining work by model section

These are work packages, not replacements for the 93-row operation ledger.

| Section | Existing bounded foundation | Work still required for full parity |
| --- | --- | --- |
| `sites` | Hierarchy reporting, native area/building/floor operations, private owned empty-hierarchy prune/recreation; selected image/AP paths | Complete schema/default/selection semantics; config deletion and retained-effect cleanup; installed and populated/physical lifecycle qualification |
| `network_settings` | Selected site settings, projected AAA, global pools and reservations; bounded owned/task recovery paths | Remaining fields/inheritance and reset transitions; IPAM resize/reparent/address-family transitions; adoption, mixed cleanup, handoff and installed qualification |
| `network_profiles` | Switching definition and creator custody; private native observation; bounded profile-site assignments and wireless profile paths | CC-02/03; adoption/prune/deletion and handoff; composite/multi-site dependencies; nonempty device consumers and installed qualification |
| `fabric` | Resource-specific parsing, planning, admission and bounded execution/recovery prerequisites | Full fabric site/zone/transit/VN/gateway/device-role dependency lifecycle; live bulk-task qualification, physical postconditions and cleanup; lab fabric resources required |
| `templates` | Projects, regular/composite definitions, version/content dependencies and bounded deployment machinery | Full upstream field/default and composite graph coverage; version retirement, ownership/deletion/handoff; installed and physical deployment qualification |
| `inventory` | Inventory reporting, discovery/credential lifecycle, selected tag/assignment and provisioning protocols | CC-04; complete PnP/provision/RMA and mixed-device transitions; native cleanup, handoff and installed physical recovery across supported operations |
| `wireless` | Selected definition guards/task recovery, owned tag lifecycle, AP planning and bounded position/radio/type operations | Remaining SSID/profile/policy/AP/WLC semantics and catalogues; trustworthy creator custody where unavailable; destructive lifecycle, associations and installed physical deployment |
| `lan_automation` | Shared reservation/group journal and resource-specific LAN/link protocols | Installed start/stop/link composition and device postconditions; partial acquisition/cancellation and uncertain sessions; real topology, scale and cleanup qualification |
| `system_settings` | Bounded external-authentication and policy-server configuration with projected credentials | Remaining schema/catalogue and reset combinations; real ISE/authentication behavior, ownership/deletion, rotation and installed recovery |
| `authentication_templates` | Reporting and bounded fabric authentication-template definitions | Remaining field/default and scope semantics; consumer-safe transitions/deletion and installed fabric/device qualification |
| `feature_templates` | Selected wireless feature definitions; advanced CleanAir/FRA owned lifecycle | Remaining types/fields and dependencies; parent deletion/handoff; installed workers and real wireless-device effect verification |

## Qualification contract for each resource

Close a resource only when its applicable upstream semantics and operations are
accounted for. Record unsupported API/platform combinations explicitly rather
than silently omitting them from a parity claim.

1. **Model:** pin resource/field mappings, defaults, null/omission behavior,
   identity, selectors, catalogues, dependency edges and input provenance.
2. **Observe and plan:** prove complete reads, pagination, stable native identity,
   deterministic plans, scope conflicts and zero mutation in Report mode.
3. **Execute:** reuse admission, journal and one-use dispatch; bind original task,
   source, owner and dependencies; independently verify resulting native state.
4. **Recover:** test taskless uncertainty, accepted/lost responses, failed/pending
   children, restart, source/key changes and stale actors without replay.
5. **Own and retire:** qualify capacity, receipts, adoption policy, Retain/Delete,
   consumer-safe prune, config/controller deletion, handoff and recreation.
6. **Deploy and prove:** use real API-server enforcement, then installed actors
   and required physical targets. Capture versions, sanitized evidence, exact
   cleanup and any retained fences.

Do not promote a section's `configurationRead`, `plan`, `apply` or `prune` flag
from section recognition or one successful resource. Use the compiled
`controller-report --require-config-parity` gate for the selected model; its
nonzero incomplete-parity result is expected during alpha.

## Ownership, evidence and forecasting

Assign a maintainer and reviewer when taking a roadmap item; these documents do
not presume acceptance by the ND maintainer or another contributor. Each closure
should identify source revision, tests, native/installed environment, remaining
restrictions and cleanup outcome in the resource ledger. Shared-boundary changes
need review from that boundary's maintainers.

The earlier estimate of another 10–20 working days is provisional and depends on
lab availability and defects found in qualification. It is not an acceptance
criterion or delivery commitment. Forecast against remaining resource gates and
required hardware; report blocked physical qualifications separately from code
work. Inventory coverage alone must never be presented as goal completion.
