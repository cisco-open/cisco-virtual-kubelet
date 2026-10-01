# C0 `.103` lost-activation outcome audit

Date: 1 October 2026. All times UTC. This record contains no credentials,
tokens or private key material.

## Identity and retained safety state

| Object | Bound identity |
| --- | --- |
| Device | `cvk-live/cat9k-lab-103`, UID `782dc4b2-7e43-4188-8436-87633f0bf4fb` |
| Node | `cat9k-lab-103`, UID `404b46d4-fcb4-4fd1-bdda-b499d0fe3d83` |
| Uncertain leaf | `roadmap-noreboot-20261001-103-cat9k-lab-103-8a61c53c`, UID `f09cde0b-f678-454c-a9c6-c9a27619cc10` |
| Reservation | `8a61c53c6958c6406b9fbed931735353` |
| Mutation Lease | `cvk-device-94384ab3a069a1ea-device-disruptive-mutation-9e53c76c`, UID `3f7f3950-0b2d-490d-8ff5-220c37cda14b` |
| Lease holder | `software-upgrade/f09cde0b-f678-454c-a9c6-c9a27619cc10` |
| Network worker used for audit | Pod UID `39fe4c0f-f4bf-47c7-8899-8697e814b56b` |

Before the audit, the leaf remained `Failed/ActivationOutcomeUnknown`, with
validated target `17.18.03.0.5496.1776157760`, previous version
`17.18.02.0.4112.1766116039`, source digest
`sha256:df6055e4e1e88135b311998d721ff6d20a94a475113c1ff678fb65d14dc11049`
and a durable primary-activation request at 17:08:34. The maintenance
`NoSchedule` taint and disruptive Lease remained held. No retry, activation,
reload, configuration write or manual fence removal was performed.

## Read-only IOS-XE view

DeviceOperation `roadmap-c0-install-audit-103`, UID
`dbeadf5e-a759-4dce-9216-b7d0287f2088`, was created before the manager bound
it to the exact device and network-worker identities above. It completed at
17:50:16.

| Command | Relevant result |
| --- | --- |
| `show version` | IOS XE 17.18.03 / 17.18.3; system returned to ROM by `Image Install`; last reload reason `Image Install`; uptime 35 minutes; install mode; system image `flash:packages.conf` |
| `show boot` | Next-reload boot variable `flash:packages.conf`; manual boot disabled |
| `show install summary` | Image `17.18.03.0.5496` is activated and committed (`C`); auto-abort timer inactive |
| `show install log detail` | Unsupported syntax on this device; the invalid-command result is retained rather than treated as evidence |

The reload timing is consistent with the lost activation interval. These
commands establish the running, committed and next-boot selection but do not
retroactively turn `NoReboot` into a preparation-only operation.

## Read-only secure gNOI view

DeviceOperation `roadmap-c0-gnoi-verify-103`, UID
`bcc6182a-3979-416f-907f-8923c47d3806`, completed at 17:51:01. Secure
`OS.Verify` returned:

```text
Version: 17.18.03.0.5496.1776157760
ActivationFailMessage: ""
IndividualSupervisorInstall: false
Standby.State: UNSUPPORTED
```

The gNOI version exactly matches the leaf's validated target. The physical
switch and gNOI views therefore give conclusive correlated evidence that the
mutation succeeded despite the lost RPC response.

## Reconciliation decision

The Kubernetes Node still reported kernel version 17.18.2 while Ready=True.
That value originates from cached provider inventory and is not authoritative
for settling software lifecycle. Recovery must use the fresh secure gNOI
result and the exact retained identities above.

The implementation added after this audit permits only the following terminal
correction:

```text
Failed/ActivationOutcomeUnknown
  -- fresh exact-target OS.Verify + exact manager claims/binding -->
Succeeded/ActivationOutcomeRecovered
```

It never replays `OS.Activate`. Old, conflicting or unavailable Verify data,
an activation-failure message, supervisor-specific requirements, stale
manager control, incomplete mutation claims or changed object identity retain
the quarantine. The fence was deliberately left in place at evidence-capture
time so the candidate can prove that audited controller path end to end.
