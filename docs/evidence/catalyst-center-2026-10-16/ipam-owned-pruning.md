# Owned IPAM source pruning and explicit reintroduction

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.
Pinned NetAsCode model: 0.5.0.

## Execution contract

A pure-IPAM `network_settings` Apply config may now select `prunePolicy: Delete`.
Omitting a receipt-owned reservation or global pool schedules exact-UUID deletion.
Explicit `ip_pools: []` requests cleanup of all allocation resources owned by that
config. `Retain` preserves omitted resources; explicit empty pools now also parse
for ordinary Retain Apply and Report. The bounded compiler requires the explicit
list and rejects missing/null pools or unknown fields. Upstream omitted-key
default-empty semantics remain a parity gap; this restriction is not a claim
that omitted input is invalid in the upstream model. The follow-on example is `ipam-prune-apply.yaml`; keep the original
config UID/name/namespace that created the ownership receipts.

Pruning reuses the existing IPAM executor, controller journal, single-use native
write closure, transport/session, task reader, atomic action writer and receipt
release guard. It introduces no new public API fields, controller, task worker or
ownership database. Normal writes and pruning use the same acknowledgement
parser. Reservations are removed before global parents. A kept or unowned child
reservation prevents parent removal and blocks the prune cohort before its first
write. Ordinary imported allocations are not adopted or removed.

The planner resolves every obsolete owned UUID against native name/site identity,
checks the complete allocation graph and known gateway/secondary-pool/LAN-session
references, and rejects partial/unknown dependency evidence. It repeats the owned
cohort, parent and site snapshots immediately before submission, then checks the
current Submitting operation/source/owner/capacity guard. Relevant identity,
field, dependency or authority changes consume the plan without another native
attempt. Unrelated site/gateway changes and other configs' receipts are retained
without creating false ownership conflicts or stale-plan failures.

Cisco's [global pool delete contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/delete-a-global-ip-address-pool/)
requires child reservations to be absent. The [subpool release contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/release-an-ip-address-subpool/)
removes it from the site and its children and refuses release while assigned
addresses are in use. CVK uses that backend constraint instead of inventing an
IPv6 usage-counter interpretation. Known reference preflight is additional
protection; it is not a transaction with independent external API clients.
A native rejection, failed task or lost acknowledgement retains the journal and
ownership evidence and never authorizes blind retry.

An acknowledged Delete requires the original creation/update receipt, complete
native task success, repeated UUID/name absence and a consistent remaining
allocation/site graph. Verified and the deletion tombstone are published atomically.
A tombstone cannot schedule another delete or a same-revision recreation. A later
explicit source revision may recreate the allocation, using a fresh creation task
and verified native UUID/parents. This can work even if a backend reuses an ID;
name matching alone is not creation proof. Each logical allocation retains one
current result receipt.

## Native public flow

`TestLiveIPAMOwnedPruneManifest` passed with race detection in 111.77s (package
113.804s), using real Catalyst Center APIs and the public config reconciler.
The hierarchy was `Global/CVK-Q-20261016-000839-000000000` (UTC fixture name).
Kubernetes persistence was simulated through serialized checkpoints and fresh
reconciler instances; this is not an installed-worker qualification.

The six source revisions performed initial dual-stack creation, IPv6 parent DNS
and reservation gateway updates, an IPv4-only reservation revision preserving
IPv6, whole-resource pruning, explicit recreation, then final pruning. Every one
of 15 acknowledged operations was restored as Unknown and recovered without
resubmission. There were six POSTs, three PUTs and six DELETEs, all allocation
mutations driven through the public reconciler. Restarted no-op passes added no
requests. Three deletion tombstones remained after verified final cleanup.

| Revision | Action | Allocation | Native UUID | Task |
| --- | --- | --- | --- | --- |
| 1 | Create | v4 | `da2c34e5-f193-495a-9f3c-b84908d1f460` | `01a1420a-388c-7ee5-897a-0dde83e55c19` |
| 1 | Create | v6 | `3d1055c0-a00e-498c-be7a-2544bff58224` | `01a1420a-4fc2-7e51-bcdf-fb77742efd3d` |
| 1 | Create | dual | `5ef4acf7-afaa-4a9a-8003-37c72a835222` | `01a1420a-668a-7dfa-876a-d6cfa8f8624c` |
| 2 | Update | v6 | `3d1055c0-a00e-498c-be7a-2544bff58224` | `01a1420a-84d1-7a4d-ab75-afe9129d7021` |
| 2 | Update | dual | `5ef4acf7-afaa-4a9a-8003-37c72a835222` | `01a1420a-9b81-733f-9979-3cf07d53ab99` |
| 3 | Update | dual | `5ef4acf7-afaa-4a9a-8003-37c72a835222` | `01a1420a-ba02-72c4-a017-08ad052fba3e` |
| 4 | Delete | dual | `5ef4acf7-afaa-4a9a-8003-37c72a835222` | `01a1420a-e041-7fe4-a14f-2d0a2f94f138` |
| 4 | Delete | v4 | `da2c34e5-f193-495a-9f3c-b84908d1f460` | `01a1420a-f706-7fb3-bc54-52d92b60f642` |
| 4 | Delete | v6 | `3d1055c0-a00e-498c-be7a-2544bff58224` | `01a1420b-0d26-7877-893e-2f31831856c2` |
| 5 | Create | v4 | `f0b74e9f-7eab-45c7-bf45-198fe14674e8` | `01a1420b-28cd-7b2f-bd95-3245f4437a7e` |
| 5 | Create | v6 | `e6d88cc5-6cb4-4257-9995-16ca706d6082` | `01a1420b-400a-7bb3-a6a7-93cd3f6743ab` |
| 5 | Create | dual | `7ab11304-69fd-43d2-b9c1-3781a024b5c5` | `01a1420b-5640-76f8-a633-c51bf0077b3a` |
| 6 | Delete | dual | `7ab11304-69fd-43d2-b9c1-3781a024b5c5` | `01a1420b-73e6-7287-a8ba-c4db2c33b2da` |
| 6 | Delete | v4 | `f0b74e9f-7eab-45c7-bf45-198fe14674e8` | `01a1420b-8a19-70f9-b177-17e983acb74c` |
| 6 | Delete | v6 | `e6d88cc5-6cb4-4257-9995-16ca706d6082` | `01a1420b-a065-7193-8fc8-c1167565cc47` |

Original global-pool/reservation baselines were restored, followed by verified
floor/building/area cleanup. The private completion record reports cleaned=true,
publicPrune=true and knownTaskRecoveries=15. No device assignment, provisioning or
software upgrade was submitted. The live branch uses a request guard restricted
to nonce-named allocations, known site/UUID bindings and an explicit mutation
budget.

Log: `/tmp/cvk-ipam-prune-native-final.log`.
Private source, 15 Accepted checkpoints, initial/deleted/reintroduced/final result
receipts, native selected readback, baselines and completion record:
`/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-ipam-dualstack-407013539/`.
These host-local artifacts contain public configuration metadata, not credentials
or tokens. Their directory/file permissions follow the existing 0700/0600 harness.

## Qualification failure and exact recovery

The first attempt correctly completed its first prune and explicit recreation.
Its last source revision reused a Go map that JSON restoration had populated with
pool declarations, so the product correctly retained those still-declared pools.
The harness failed its absence assertion and kept the test data. Clearing the
map before restoration fixes the alias without weakening product checks.

The retained three creation receipts were independently reverified against their
native tasks, UUIDs and selected fields, then cleaned through a fresh explicit
public prune revision with three acknowledged Unknown recoveries. Original site
UUIDs came from the actual creation task data and matching native readback; exact
floor/building/area deletion followed pool cleanup. No resource was adopted for
cleanup by name. The private recovery claim prevents replay of an uncertain run;
each site deletion has its own durable local claim. This qualification-only
recovery is not a substitute for an installed Kubernetes journal.

Recovery passed in 25.29s (package 27.326s); a later read-only resume passed in
0.45s (package 2.571s) with zero mutation attempts. Logs:
`/tmp/cvk-ipam-prune-retained-cleanup.log` and
`/tmp/cvk-ipam-prune-retained-resume.log`.
Private retained/recovery artifacts:
`/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-ipam-dualstack-1431348996/`.
Both that hierarchy and the fresh qualification hierarchy are now absent.

## Regression evidence

Public fixtures cover dual-stack prune/recreate, child-before-parent ordering,
Retain/no-op, opaque lost acknowledgement, backend-in-use rejection and changed
source fencing. Dependency cases cover primary/secondary gateway references,
unknown/incomplete gateway data, LAN references, unowned children, replaced sites,
external disappearance and preservation of other configs' results. Late native
reference/field/identity changes, pruned receipts and unrelated site/gateway
changes exercise single-use dispatch. An initial unrelated-site fixture used the
wrong parent UUID; the corrected complete hierarchy passes without relaxing the
shared site reader.

Affected IPAM/fabric/gateway/task race tests pass (78.750s), the final prune safety
matrix passes (5.882s), all IPAM/example/empty-source tests pass (8.300s), and shared
private cleanup-claim regression passes (1.904s). ND, shared provider/controller,
configuration/maintenance/mutation guards, gNOI and IOS-XE software-lifecycle checks
pass. The first complete package run failed only the example checker's blanket
nonempty-resource assertion: prune-all intentionally has zero desired resources
and derives its deletes from owned receipts. The assertion now permits only that
specific IPAM/Delete case; targeted example checks pass (2.326s). The complete
Catalyst Center package rerun passes with the race detector (613.073s;
`/tmp/cvk-ipam-prune-package-final.log`). The strict documentation build passes
(4.81s), and `git diff --check` is clean. A fresh connection check also returned
HTTP 200 for credential-based token generation and authenticated inventory count
(three devices); no credentials or tokens were logged.

A real isolated Kubernetes 1.35 API-server test now preserves Delete tombstones,
rejects stale writer CAS, gates release on the result receipt and permits only a
later explicit revision to reserve recreation. Ordinary and grouped receipt
namespaces plus the existing SSID history test pass (package 12.903s). Native task
evidence is synthetic in that schema test; the separate live test above supplies
actual API qualification. No existing lab-cluster data or runtime was altered.

## Remaining boundaries

Pruning is enabled only for pure-IPAM configs. Mixed credential/settings/fabric
pruning and automatic CR-deletion cleanup remain unsupported and are rejected
before writes. Whole-reservation/global-pool removal is supported; IPv6-family
detachment, optional-field resets, resize/reparent/type replacement and legacy
resource adoption are separate lifecycle gaps. An externally missing/replaced
owned resource requires recovery, not guessed deletion completion.

Historical LAN sessions referencing a pool remain conservatively blocking even
when their action says STOP; that text alone is not retirement proof. Unknown or
missing gateway-secondary metadata also prevents pruning. Native busy/assigned
allocation refusal was not exercised with live device traffic; fixture checks
model rejection and the implementation relies on the documented controller
constraint. Broader inherited/mixed/scale behavior, receipt-capacity lifetime
management, section-owner handoff, installed actors/API-server reconciliation and
full pinned NetAsCode parity remain pending. These results do not establish full
resource support or branch merge readiness.


## Follow-on default-empty alignment

The explicit-list requirement above describes the original qualification. The
follow-on [omitted-key implementation and native qualification](ipam-default-empty.md)
now accepts `network_settings: {}` with the pinned empty-pool meaning. Null and
unknown input remain rejected; the other lifecycle limitations still apply.
