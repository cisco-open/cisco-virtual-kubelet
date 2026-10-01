# `11ae6704` physical NoReboot boundary result

Date: 1 October 2026. Target: `cat9k-lab-103` (`198.51.100.103`).
Candidate: `cvk-tas-extentions:11ae6704`, deployed as Helm revision 118.

## Purpose

This was a single-device, workload-free investigation of the IOS-XE gNOI
preparation boundary. It was **not** an independently approved activation
implementation or a successful staged-image qualification.

The manifest selected only `topology.cisco.vk/lab-node=cat9k-lab-103`, used the
previously successful pinned 17.18.03 source, enabled the existing topology
network evidence gate, and requested `strategy: NoReboot`. The immutable plan
was frozen before its separate plan approval was added.

## Result

1. Before the candidate fix, network-enabled planning panicked because the
   planning path read `status.effectivePolicy` before it existed. Commit
   `11ae6704` fixes that by using the already parsed administrator policy.
2. With the fixed image, the plan froze as `AwaitingApproval`, then the
   controller correctly deferred leaf creation while the replacement network
   worker was not attested (`GNOIConfigurationReady=False`). It proceeded only
   after that condition returned `True`.
3. CVK completed a single gNOI OS.Install transfer of 1,249,368,115 bytes and
   persisted the activation claim before issuing `OS.Activate` with
   `NoReboot=true`.
4. IOS-XE returned a gRPC `DeadlineExceeded` for that activation request. CVK
   recorded `Failed/ActivationOutcomeUnknown`, did not replay activation, and
   retained the device-maintenance taint and mutation lease.
5. A separate read-only, secure gNOI `OS.Verify` subsequently returned running
   version `17.18.02.0.4112.1766116039`, with no activation-failure message.
6. At 17:50–17:51 UTC, after the switch had become reachable again, exact
   manager-bound read-only operations established a different, later state:
   IOS-XE `show version` reported 17.18.03, an Image Install reload and a
   35-minute uptime; `show install summary` reported 17.18.03.0.5496 as
   activated and committed; `show boot` selected `flash:packages.conf`; and
   secure gNOI `OS.Verify` reported `17.18.03.0.5496.1776157760` with no
   activation failure. The correlated record is
   [the C0 outcome audit](c0-103-activation-outcome-audit.md).

## Interpretation

The first read-only result establishes only that the device was still running
the old version at that observation time. The later correlated audit proves
that the timed-out operation ultimately changed the running and committed
software to the exact validated target. It also proves that this IOS-XE cohort
may reload after `Activate(NoReboot=true)`; therefore `NoReboot` must not be
presented as a preparation-only or no-downtime boundary.

Retaining quarantine until that conclusive later observation was correct. Do
not clear the lease manually or submit another activation. The controller
recovery must consume a fresh exact-target `OS.Verify`, validate the original
leaf/device/manager-claim identities, record an audited terminal correction
and release ownership without replaying activation. Node Ready and its stale
reported 17.18.2 remain insufficient device-inventory evidence.

This test invoked Activate, so it did **not** test E04's hold between Install
and Activate. It neither qualifies independent staging nor proves the cohort
incapable of it. E04 must first prove the device boundary using a guarded
Install-only path with no reachable activation call. E05 can then implement
and qualify the durable receipt against the proven semantics; the receipt
must not be a circular prerequisite for E04's device investigation. See the
[C0/C4 execution steps](../../topology-roadmap-execution.md#concrete-completion-queue-c0c9).

## Artifacts retained in the lab

- Rollout: `cvk-live/roadmap-noreboot-20261001-103`
- Leaf: `cvk-live/roadmap-noreboot-20261001-103-cat9k-lab-103-8a61c53c`
- Read-only verification: `cvk-live/roadmap-noreboot-verify-20261001-103`
- Mutation lease: `cvk-live/cvk-device-94384ab3a069a1ea-device-disruptive-mutation-9e53c76c`

These objects are evidence and safety fences. Keep them through the later
audited-recovery implementation and test; they must not be deleted to make a
lab snapshot appear clean.
