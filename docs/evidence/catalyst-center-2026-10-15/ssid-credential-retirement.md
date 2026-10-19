# Manifest-driven Multi-PSK retirement and retained version history

## Execution and ownership

CVK can now remove a previously applied Multi-PSK priority, reintroduce it only
above its retained version counter, and explicitly clear the Multi-PSK collection
while retaining a projected main passphrase. This uses the existing public
NetworkControllerConfig reconciliation, versioned Secret projections, single
controller operation slot, native task correlation and credential receipts.
No new provider or transport is added; the generic credential rotation helper
retains its strict existing behavior for other credential executors.

An SSID-specific transition validates the prior owner/native receipt, distinguishes
an explicitly owned collection from omission, and excludes only explicitly removed
Multi-PSK leaves from the active-version comparison. Retained active credentials
keep existing monotonic/source checks. Removal never needs to project the old
password. Main-passphrase removal and wholesale credential/authentication-mode
relinquishment remain refused.

Operations and application receipts now carry optional `ssidCredentialHistory`:
a bounded, canonical priority/version list (at most five). It retains high-water
marks after a key disappears; reintroduction must strictly advance its counter.
It contains neither passwords nor hashes of passwords. A history-bound resource
identity includes a digest of this non-secret list, so pruning any retired entry
from an operation and receipt cannot silently erase the history.

Prepare, submission-permit comparison/readback, receipt capacity, task completion,
receipt recording and journal release bind this history through the existing CAS
protocol. Capacity checks also compare with the latest durable receipt, refusing
stale plans that would lower a retired floor. Legacy receipts can derive initial
history from their applied Multi-PSK versions. Advancing history replaces the same
SSID receipt, rather than accumulating aliases; duplicate receipts, foreign owners
and native target changes remain refused by existing ownership checks.

The generated API and Helm schemas and deepcopy methods include the additive
history fields, including embedded operation records. A downgraded schema retaining
the marked resource but pruning history cannot prepare/dispatch/finish the write.

## Manifest use

Start with `examples/configs/catalyst-center/wireless-ssid-multipsk-credentials-apply.yaml`.
Then update that SAME config with
`examples/configs/catalyst-center/wireless-ssid-retire-priority4-apply.yaml`.
The follow-on preserves both config and SSID names, retains main/priority-0
versions and aliases, removes priority 4 and its SecretRef, and correctly moves
priority 0's source path to its new list index. Keep retained Secret values and
versions unchanged. The removed key need not remain projected; its controller
source alias can remain authorized for future use.

To clear all Multi-PSK entries on the owned SSID, use an explicit
`multi_psk_settings: []`, remove their SecretRefs and retain the main passphrase
intent/projection. Omission does not request removal. For reintroduction, use a
version above that priority's retained `ssidCredentialHistory` value and publish
the matching atomic version/value Secret before execution. The examples remain
disabled, without profile, AP/WLC or device association.

## Verified scope

Wireless/shared credential regression passed with race detection in **10.941 s**.
A public manifest fixture now covers priority reorder, rotation, single-key removal
with old projection files deleted, JSON journal restoration, old-counter refusal,
advanced reintroduction and explicit empty-set retirement. One active main version
and both retired priority floors remain in the final receipt, with one receipt
and no operation. Additional helper tests cover malformed/duplicate/unsorted/
pruned history, wrong-section injection, legacy migration, stale durable floors,
source/version checks and deep-copy isolation.

Schema tests passed in **2.894 s** before the final capacity guard. They inspect
all four generated/Helm schema occurrences and simulate old-schema pruning using
the Kubernetes structural schema implementation. History loss prevents preparing
a marked operation, receiving submission permission and releasing a Verified slot,
even when the matching receipt loses the same field. These tests also run in the
final wireless regression above.

API, Nexus Dashboard and provider race regressions passed (API ops 2.529 s,
API v1alpha1 2.160 s, ND 4.663 s, provider 5.130 s; config API cached).
These ran before the final SSID-only capacity guard. The full Catalyst Center adapter suite passed with race detection in
**606.378 s** on the history implementation before the final capacity read guard.
The final guard is covered by the wireless/shared credential regression and
repeated native qualification described below. This avoids treating the earlier
broad run as a test of later code.

## Initial native cycle

The first cycle passed in **38.24 s** (package 40.356 s) on an isolated disabled
WPA2_PERSONAL SSID with a main passphrase and priorities 4/0. It created the
repository example through the public reconciler, reloaded the JSON journal across
reconciliation, reordered priorities without mutation, rotated priority 4 to
version 2, removed it, refused version 2 on reintroduction, accepted version 3,
and cleared the Multi-PSK set. Exact deletion restored original SSID/profile
baselines. Native API/tasks and projection files are real; Kubernetes persistence
and worker restarts are simulated.

Fixture `CVK_PSK_ce2c84eb281f75b4`, ID
`mdi0q1zl-x1bt-s19J-ztjJ-odrLyJi4mwy3nwi0qZbwmuSYxZnq`, had one POST, four PUTs
and one DELETE. Cleanup task `01a14163-3b92-7bd8-bb4e-7d60870e28f7` completed;
repeated reads proved absence. Private evidence at
`/private/tmp/cvk-ssid-manifest-2729292107/` is mode 700/files 600. The independent
artifact audit confirmed main version 1, retired priority 0/version 1 and priority
4/version 3, no operation, exact counts and completed deletion. Generated passwords
are excluded from persisted journal/public metadata/recovery evidence.

## Final-code native cycle

After the latest-durable-history capacity guard, the complete native lifecycle
passed again in **40.09 s** (package 42.195 s). Fixture `CVK_PSK_c9aa4ca2c5df777c`,
ID `mdi0q1zl-x1bt-s19J-owfH-ngnHmMm1zgy3nZDJqZbwmuSYxZnq`, used one POST, four PUTs and one DELETE. Cleanup task
`01a14168-6274-7d3a-8cb1-ac4915802e03` completed and repeated baseline checks passed. Private
artifacts at `/private/tmp/cvk-ssid-manifest-2014743045/` were independently checked:
one receipt, main version 1, retired priorities 0/version 1 and 4/version 3, no
operation, completed deletion, and directory 700/files 600. Both native fixtures
from this stage are gone. No profile association or physical-device operation
was submitted. The later [task recovery qualification](ssid-credential-task-recovery.md) extends
bounded retirement cleanup proofs and known-delete read-only resume; native
cleanup is qualified for the original rotation and stage-2 removal.

Strict documentation build passed in 4.99 s before this evidence update.

## Isolated real API-server qualification

`TestEnvtestSSIDCredentialHistoryPersistence` passed with race detection against
Kubernetes 1.35.0 in **5.94 s** (package 8.230 s). It installs the actual generated
NetworkController CRD into a disposable envtest API server and etcd, creates a
controller, writes operation/application history through the status subresource,
and reads it back without pruning. API admission rejects out-of-range priorities,
zero versions and duplicate priority map keys. The server is stopped afterward.
Status/task evidence in this test is synthetic; no controller worker, device API
or lab cluster is involved. This supplements, rather than replaces, the separate
native API/task and public-reconciler qualification.

## Remaining limits

This qualifies logical disabled definitions and write-only version/task receipts,
not actual client authentication or WLC/AP application of passwords. Lab
worker/projected-volume and lab cluster schema rollout remain pending; isolated
API-server qualification does not run or migrate the lab worker. More key/type/auth/cipher
combinations, complete credential/auth-mode transitions, adoption and pruning also
remain pending. The existing native pre-submit reads cannot provide appliance-side
atomic exclusion against an external writer. Full pinned NetAsCode execution
parity is still incomplete.
