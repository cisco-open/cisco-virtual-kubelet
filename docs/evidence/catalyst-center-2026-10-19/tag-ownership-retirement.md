# Durable tag ownership retirement

Subsequent qualification: [same-section owner handoff](tag-owner-handoff.md)
now closes the original-plus-two-successor native flow. The scope and source
hashes below describe this earlier phase.

Tag CR cleanup now persists a retirement witness on the NetworkController before
publishing the config completion proof. The pure manager callback requires that
witness for owned Delete-policy configurations. This is a prerequisite for a
future ownership handoff; it grants no successor ownership or submission permit.

The witness binds the pinned model version, controller/config UID and generation,
selected section set, exact spec/deletion digests, complete closed action history
and existing section claims. Publication repeats native absence and fresh
config/controller identity checks, then uses the existing status CAS. Original
creator/update/deletion receipts and section claims remain unchanged. Manager
callbacks stay pure and the worker still has no config metadata write rights.
The shared metadata cleanup worker exposes one optional retirement callback;
feature cleanup leaves it unset and keeps its existing behavior.

A refused status write retains the finalizer. A lost acknowledgement recovers the
already-persisted canonical witness without another status publication or native
request. A late resurrection between initial cleanup observation and retirement
publication prevents authority from being recorded. Switching to Retain
invalidates an existing Delete retirement witness before Retain completion;
Retain never grants transfer authority.

## Qualification

The controlled/real-API lifecycle and integrity gate passed with 186 passing
events. The compatibility gate passed with 947 events, including existing tag
prune/recreation/receipt rules, configuration guards, discovery cleanup and
handoff, feature-template deletion, descriptors and repository examples. Counts
include parent and package pass events. ND, direct software lifecycle,
maintenance, manager/controller, SWIM/Reload/XFSU/fallback checks and main build
also passed.

Three native static-tag cycles passed through an isolated Kubernetes API. Each
used `prunePolicy: Retain`, `deletionPolicy: Delete`, one POST/PUT/DELETE and
recovery of all three original tasks. The harness first verified that the
registered manager contract preserved its unrelated finalizer, then removed that
qualifier-owned finalizer so the old CR actually disappeared. The retirement
witness, old UID's claims and exact receipt ledger survived unchanged. Full native
catalog baselines were restored; no device operation was permitted by the strict
transport guard.

| Scope | Native UUID | Old CR absent | Ownership and receipts |
| --- | --- | --- | --- |
| inventory | `c2796a54-f1ee-4444-aa69-6026e4107ba1` | Verified | Unchanged |
| combined | `85b9191d-2f59-4647-80c5-d589fb24f299` | Verified | Unchanged |
| templates | `ecef643b-89f3-460b-9310-1c7d4f8ae52f` | Verified | Unchanged |

See [qualification data](tag-ownership-retirement.json) and the selected
[persisted witness after CR removal](tag-ownership-retirement-native-records/inventory/retirement-proof-survives.json).
This qualifies a durable prerequisite, not an installed manager/worker rollout.

## Remaining handoff work

A replacement configuration remains blocked by the old ownership claim. Actual
handoff still needs an old-CR absence check, repeated native absence, sealed
retired-history preservation, successor input binding and atomic claim transfer
through the existing journal/status foundation. Subsequent creation must reject
old native UUID reuse, and missing/replaced history must keep the fence. Empty
and repeated owner cycles, section changes, installed actors and legacy owners
whose CR disappeared without a witness need separate implementation and
qualification. The full pinned NetAsCode model remains incomplete.
