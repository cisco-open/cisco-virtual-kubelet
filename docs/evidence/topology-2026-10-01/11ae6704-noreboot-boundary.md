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

## Interpretation

The read-only result establishes only that the device was still running the
old version at the time of observation. It does **not** prove whether the
timed-out NoReboot request reached the device, whether a next-boot selection
was made, or whether the installed image is durable and content-identical.

Accordingly, the retained quarantine is correct. Do not clear the lease or
submit an activation/retry manually. This C9K/IOS-XE cohort is not eligible
for E04/E05/E06 positive staging claims until CVK has a durable receipt,
explicit audit/recovery flow, and a platform procedure that can prove the
installed/next-boot state after a lost response.

## Artifacts retained in the lab

- Rollout: `cvk-live/roadmap-noreboot-20261001-103`
- Leaf: `cvk-live/roadmap-noreboot-20261001-103-cat9k-lab-103-8a61c53c`
- Read-only verification: `cvk-live/roadmap-noreboot-verify-20261001-103`
- Mutation lease: `cvk-live/cvk-device-94384ab3a069a1ea-device-disruptive-mutation-9e53c76c`

These objects are evidence and safety fences. Keep them through the later
audited-recovery implementation and test; they must not be deleted to make a
lab snapshot appear clean.
