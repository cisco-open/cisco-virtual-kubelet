# Manifest-driven remediation qualification

Branch: `pr/johalley/catc-support`.

The implementation is opt-in through `image.sources[].catalystCenter.preparation`.
See [the policy and flow](../../../controllers/catalyst-center-remediation.md)
and the examples under `examples/configs/catalyst-center/`.

## Test coverage

- Full repository regression: `go test -race ./...`.
- Kubernetes API/ownership: SWIM handoff and rollout leaf round-trip envtests.
- Runtime factory forwarding: current transport resolution, unsupported adapters,
  archive read/remove and native observation forwarding.
- Cleanup guards: exact invalidated receipt, protected current/target/native
  references, installed images, application inventory, cumulative budgets,
  exact streamed SHA-256 and size, boot package references.
- SCP framing: single exact file, size/name changes, truncated transfers, remote
  errors, extra entries, directories and bounded headers; CLI path injection.
- Controller state machine: preparation before both stages, sync completion before
  readiness, durable ambiguous-outcome handling without replay, exact fresh device
  child-task correlation and no mutation retry after authentication failure.

## Live setup

- Switch: `cat9k-lab-101`, `198.51.100.101`, device UID
  `20f3e68e-b127-4f2e-9a36-b66945135051`.
- Rollout: `cvk-live/catc-auto-remediation-20261008-101`.
- Leaf: `catc-auto-remediation-20261008-101-cat9k-lab-101-987e78f4`.
- Campaign UID: `1003bf72-8938-4e7b-be8f-54b81f892c31`.
- Leaf UID: `da660b9f-3b49-433d-bf19-a45782261561`.
- Frozen plan: `sha256:89afdcce47ec0073461bd043c62195edb59d8c4bc826444135d30ea9f543572a`.
- Policy UID: `7ed30098-ca6b-452f-91ad-322a08e5e3b2`.
- Policy SHA-256: `dd96201b78b562029de5efb7f8a12bbef569b3947d55b82de2e6f28e8e78bb88`.
- Limits: one file, 1,610,612,736 bytes; free-space threshold 2,936,012,800
  bytes plus 134,217,728 bytes headroom.
- Build: `cvk-catc-handoff:20261008-12`, binary SHA-256
  `ea7b39e002c8e90cea16f30adb975f417cb4949f536a21a3f5fcfca3b8c39242`.

The verified 17.18.02 archive retained from the earlier supervised run was copied
back to flash as a test fixture while the node was empty and its mutation lease
idle. A read-only probe through the new constrained SCP reader independently
returned SHA-256 `c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355`,
matching the invalidated receipt and retained backup. Fixture restoration is
lab setup, not automatic remediation evidence.

The target remains 17.18.04, already committed on this switch. This test qualifies
the new preparation/synchronization path; it is not a new version-change test.
The earlier supervised run remains the evidence for 17.18.03 → 17.18.04.

Build 10 stopped before cleanup because the runtime lifecycle wrapper omitted
capability forwarding. Build 11 fixes that omission; its factory-path regression
test is included. Build 12 also qualifies XE’s automatically cataloged
`Present` / single `IMG/new` archive shape while excluding installed, default,
added, unverified and extracted-package cases. No manual cleanup or synchronization is used to advance this
manifest-driven run.

## Observed automatic remediation

The first preparation completed automatically at `2026-10-08T18:50:52Z`:

- Plan SHA-256: `4862aa5b62338bf8bac6f3ed35f4d1f8befaec8749792a3865ecc83368468711`.
- Removed `flash:gNOI_iosxe_17.18.02.0.4112.1766116039.bin`, 1,247,897,709 bytes,
  backed by invalidated receipt UID `fcd5a376-5524-41f5-a939-4de69696fee6`.
- Independent native free-space observations: 2,032,709,632 → 3,281,833,984 bytes.
- Inventory sync submitted automatically at `18:51:08Z`, task
  `01a11cdb-d703-709d-9ebe-3516d7205a96`. The exact device child
  `01a11cdb-d72a-7ba5-ba08-23e8577a7339` reported
  `Synced device: 198.51.100.101 Status: SUCCESS`; CVK accepted completion at
  `18:53:41Z`, after the appliance's ahead-of-cluster timestamp became current.
- Fresh readiness task `01a11cde-30b6-784b-9539-08df1a4f0280` reported the Flash
  check successful, with 3,129 MB available against 2,620 MB required.

The same-version xFSU readiness check returned a warning. CVK did not waive it,
distribute, activate or claim a completed upgrade. An explicit ordinary rollout
cancellation was requested. The controller fenced future dispatch as
`PreparationCancelled`; the XE worker reverified its healthy unchanged baseline
and archive absence before recording `Cancelled`. Unknown/incomplete outcomes
cannot enter this narrow settlement path. The existing canonical lease, manager
health soak and maintenance settlement remain authoritative.

Build 13 added preparation-only cancellation; build 14 makes the quarantine guard
recognize only that conclusive worker outcome instead of reacquiring a bare
quarantine lease. Its native settlement test also checks the shared quarantine
guard, including refusal to release when completion evidence is absent.
Build 15 extends the existing manager-owned worker recovery path for that exact
settled preparation-only cancellation, checking the handoff, device/Node/lease
UIDs, current cancellation revision, and topology-lock acquisition. It retains
all scheduling guards while replacing a stale worker. The manager receives only
`get` permission on handoff journals, with a regression test rejecting write
permissions. A direct Deployment image
update during investigation was rejected by the protected-worker admission
policy; no policy was disabled and no service-account impersonation was used.
Final build: `cvk-catc-handoff:20261008-15`; binary SHA-256
`477977ea9245092b62c782027dd1fc28643d220e5137e531260941b03311c0d0`.

This evidence qualifies automatic cleanup and synchronization before distribution.
Both-stage sequencing, no-op preparation and ambiguous-outcome behavior have
regression coverage. A complete version-changing automatic-remediation cycle,
including the pre-activation stage, remains a separate live qualification.
Ubuntu16 artifacts: `/tmp/cvk-swim-auto/`.


## Final settlement

At `2026-10-08T19:29:31Z`, the rollout was `Cancelled` with every reservation
settled, and device maintenance was `Settled`. `.101` was Ready, schedulable,
without taints and with GNOIConfigurationReady=True. The software-upgrade lease
holder was released; the final snapshot caught an ordinary `device-write` holder,
not a retained SWIM mutation. Both existing workloads on `.100` remained Running
and Ready with zero restarts. Helm revision 183 contains build 15 and the read-only
manager journal permission.

`cleanup-completed.json`, `sync-1-tree.json`, `readiness-1.json` and
`readiness-task.json` record automatic actions and the actual readiness blocker.
`final-state.json` and `final-health.json` record the settled outcome. The approved
rollout, immutable policy and ordinary cancellation patch are retained alongside.
No manual file deletion, manual inventory synchronization, status fabrication,
force-release or admission-policy bypass was used to advance this qualification.
