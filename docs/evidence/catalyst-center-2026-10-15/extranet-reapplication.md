# Extranet repeated updates and additive repair

## Scope

On `pr/johalley/catc-support`, new verified extranet updates use one stable
`extranet-expansion:<native-policy-id>` receipt per native policy. The original
creation receipt remains separate and immutable. Replacement uses the existing
shared `PersistReplacingVerifiedGroupReceipt` writer; no shared protocol, store,
provider foundation, ND or device upgrade implementation was changed.

The writer checks exact predecessor identity and retirement of all predecessor
reservations before and after publication authorization. Settled rows alone are
insufficient: the existing retirement path must remove them first. Legacy
state-hash update receipts remain retained; this change does not compact them.

## Local qualification

`TestExtranetPublicRepeatedUpdates` exercises public reconciliation across:

1. Create with one subscriber.
2. Add a second subscriber.
3. Add a third subscriber, replacing only the previous update receipt.
4. Repair external additive drift by restoring the declared third subscriber.

All four operations keep one native policy UUID. The test simulates manager and
device actors, reuses real fixture lease/worker object UIDs, advances lease
incarnations, completes terminal release/settlement, and calls the shared
reservation retirement path. Final matching-source observations reach InSync
without additional native requests. Receipt count remains two: original creation
and latest update. No membership-removing rollback is enabled.

Two negative variants retain settled predecessor rows or introduce an old row
during publication authorization. Both preserve the old receipt and Verifying
journal without replaying native mutation. The retained-row variant resumes once
the shared retirement path removes the exact original rows. The late foreign row
is deliberately left fenced for investigation in its isolated fake client.

Initial diagnosis found the test actor installer processing historical rows and
therefore overwriting the current maintenance session. It now selects the current
controller and operation before simulating actor status; production authority
checks were preserved.

## Native boundary

Read-only authentication and inventory on 2026-10-15 succeeded through the existing
local tunnel. The first bounded page returned HTTP 200 for all three endpoints:
zero fabric sites, two L3 virtual networks, and zero extranet policies. No isolated
fabric fixture was selected, and no native mutation was attempted. These local
cycles do not attest installed actors or accepted native fabric execution.

## Validation

See the accompanying `extranet-reapplication-*.log` files for the affected race
tests and protocol checks. The previous full Catalyst Center suite remains recorded
in `extranet-public-recovery-full.log`; it predates this receipt-key change.
This revision runs all extranet/SWIM tests, shared foundations, ND, direct upgrades
and targeted manager protocol checks rather than claiming a new full-suite run.

Remaining work includes native and installed-actor qualification, legacy receipt
compaction, destructive changes/provider replacement/deletion, omitted/global
scope and mixed prerequisite composition. Overall NetAsCode parity remains partial.
