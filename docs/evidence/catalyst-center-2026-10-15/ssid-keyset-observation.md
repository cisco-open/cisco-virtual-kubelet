# SSID key-set observation and explicit-empty qualification

Multi-PSK entries use priority as identity. The public inventory reader now sorts
sanitized entries by priority; it still removes returned passphrases and rejects
duplicate/invalid priorities. This avoids a false stale-plan refusal when native
readback changes only list order between planning and submission. Manifest order
and credential projection paths remain untouched.

An explicitly owned empty `multi_psk_settings: []` now matches either a sanitized
omitted collection or an actual empty array. A populated set never matches empty
intent. Omitted intent still leaves collection ownership unspecified. Malformed
readback is not accepted as an empty set. The payload continues to send an actual
empty JSON array when the manifest explicitly owns it.

The new repository example is
`examples/configs/catalyst-center/wireless-ssid-empty-multipsk-apply.yaml`.
It uses the existing versioned main-passphrase projection and stays disabled,
with no profile association or device provisioning. No controller/foundation,
ND, direct-device or SWIM behavior is changed.

## Verification

Wireless and shared credential regression passed with race detection in 10.336
seconds. Tests cover optional/empty/populated/malformed collection observations,
unchanged omitted ownership, secret exclusion and canonical native priority order.
A submission test reverses API priority order between planning and preflight:
the legitimate selected-key rotation still sends exactly one PUT. Single-priority
and whole-set ledger removal remain refused by the monotonic version guard.

The explicit-empty repository example passed against Catalyst Center in 18.50
seconds (package 20.394 seconds), through public reconciliation with simulated
Kubernetes persistence/JSON journal restarts and real secret projection files,
appliance API and native tasks. The outgoing transport guard requires the empty
array on both POST and PUT. Create, accepted-task restart, stale-projection refusal,
same-ID main-key rotation, no replay and exact cleanup passed.

Fixture `CVK_PSK_a111d3662adefb86`, ID
`mdi0q1zl-x1bt-s19H-mteX-zdm2nJjHzgvMyJG2qZbwmuSYxZnq`, had exactly one POST,
one PUT and one DELETE. Cleanup task `01a1415a-49ba-7731-85b4-2735e500ec67`
completed, repeated readback proved absence, and original SSID/profile baselines
were restored. Private metadata is at
`/private/tmp/cvk-ssid-manifest-2754385547/` (directory 700/files 600). Final
journal has no active operation. Generated passwords are not retained in recovery
evidence or copied to this repository.

## Still required: credential retirement

This closes observation/convergence prerequisites, not previously applied key
removal. Current receipts retain the active credential set. Simply dropping a
receipt entry would forget the retired priority's last version/source and allow
an old counter on reintroduction. The existing guard therefore remains strict.

Retirement needs durable, bounded history tied to the same config owner and
native SSID identity, explicit owned-set intent (omission is not deletion),
retained per-priority counters, and validated source/operation/receipt correlation.
A removed source must not be required to continue projecting its old password.
Retired history must survive restart, task uncertainty and reintroduction; schema
pruning of required history must refuse execution/completion rather than erase it.
History and completed native removal must be persisted with the receipt through
existing journal CAS/recovery, without weakening unrelated credential executors.

Installed worker/CRD/projected-volume qualification, client authentication,
additional key/auth/cipher combinations, adoption and pruning remain pending.
Native omission/null variants are fixture-covered; the live cycle qualifies the
lab's own returned shape and does not attest every appliance release. Full pinned
NetAsCode execution parity remains incomplete.

The later [retirement implementation](ssid-credential-retirement.md) closes the
previously pending bounded Multi-PSK remove/return path with retained main
passphrase; full credential/auth-mode transitions remain pending.
