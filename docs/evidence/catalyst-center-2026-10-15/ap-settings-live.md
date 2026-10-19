# AP settings and multiple-calendar qualification

Pinned source: NetAsCode Catalyst Center 0.5.0, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`, `cc_wireless.tf` AP profiles.
This extends [calendar qualification](ap-calendar-live.md) through the existing
compiled wireless executor; no alternate controller, transport or credential path
was introduced.

## Native execution and convergence

The isolated compiled-model cycle creates two absent, randomly named power
profiles before its AP profile. Task completion, exact-UUID metadata readback,
no-op planning and fresh compilation are checked at each stage:

- DAILY, WEEKLY (Monday/Friday), MONTHLY (2/12), two nonoverlapping DAILY
  calendars on distinct power profiles, reversed row order, and explicit clear.
- Static power selection with client limit 10.
- CDP, aWIPS, forensic aWIPS, PMF denial and rogue detection enabled, with
  RSSI -80, transient interval 120 and report interval 20.
- Mesh enabled with bridge group CVKBGN, backhaul client access, range 200,
  both backhaul rates 802.11ax, RAP downlink 5 GHz and controller offset 1:30.
- Reset of modeled default settings.

Reordering submitted one update, then converged; this is not an order-independent
no-write claim. Observed optional fields remain subject to the existing merge
semantics; resetting defaults does not prove removal of every omitted optional
field. Each mutation rechecks unrelated AP/power baselines and complete recognized
wireless profile/tag references. Cleanup deletes only learned UUIDs, AP before
power definitions, then proves restoration of public baselines. No assignment,
WLC provisioning, AP operation or switch upgrade was submitted.

## Discovered constraints and fixes

An initial attempt to change an existing AP from ordinary to remote-worker mode
failed HTTP 400, code `NCND85057`: `remoteWorkerEnabled modification is not allowed.`
See [initial failure](ap-settings-initial-failure.log),
[diagnostic](ap-settings-diagnostic.log) and
[verified retained-fixture cleanup](ap-settings-retained-cleanup.log).

The [Update AP Profile schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/update-ap-profile-by-id/)
contains this field; schema presence alone did not establish that the appliance
accepts a mode transition. CVK now rejects both directions of mode change before
planning any mutation, including earlier power prerequisites in a combined model.
A missing observed mode holds rather than guessing. Ordinary public manifest
reconciliation reports `ImmutableAPProfileField` with guidance to use a different
profile name; existing objects are retained. Accepted/in-flight operations retain
their original recovery fences. Fixtures cover both directions, unknown mode,
mutable updates, prospective prerequisite refusal and public status/no-journal.

A separate remote-worker profile was accepted at creation but initially failed
CVK readback because the appliance returned an empty `apPowerProfileName`.
The decoder now accepts this observation-only sentinel, while manifests still
reject an empty power-profile name. No arbitrary-field or credential allowlist
was expanded. After that fix, the exact retained remote-worker UUID converged,
accepted a client-limit update in the same mode, verified and was deleted;
[recovery evidence](ap-settings-remote-recovery.log) records that outcome.
The [failed readback](ap-settings-readback-failure.log) is retained for traceability.

## Limits

These are native definition/task/public-metadata tests. They do not prove device
remote-worker behavior, radio/mesh security effects, calendar execution on an AP,
credential authentication, overlapping-calendar semantics, assignment, adoption,
pruning or installed Kubernetes actor recovery. The existing CVK API readiness
probe still returns EOF. Broader resource-level gaps remain in the
[parity ledger](../../controllers/catalyst-center-resource-parity.md).

## Fresh verification after fixes

The [fresh native run](ap-settings-live.log) passed in **40.591s**: calendar/settings
cycle **33.94s**, separate remote-worker create/update/verify/cleanup **4.78s**.
Both started with absent random names and restored public baselines. The
[manager/worker role regression](ap-settings-runtime.log) passed in **4.005s**.
The [readiness probe](ap-settings-cluster.log) still returned EOF.

Strict documentation rebuild passed in **4.17s**. The revised two-power/two-calendar
example parses as YAML and retains `catc-wireless-ap-apply` as its existing config
owner/name. `git diff --check` passed. The example is a replacement for the single
AP configuration example, not an additional competing owner.

The [full affected race suite](ap-settings-regression.log) passed: Catalyst Center
**510.198s**; nacmodel, controller configuration, configuration reservations,
Nexus Dashboard, controller and managed protocols passed from cache. Production
code was held unchanged during that run. This is regression evidence for those
packages, not a new live SWIM or direct-device-upgrade qualification.
