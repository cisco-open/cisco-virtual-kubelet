# Catalyst Center normal-reload qualification — 9 October 2026

Branch: `pr/johalley/catc-support`. **Live upgrade succeeded.** By 05:35:14 UTC,
the rollout, leaf upgrade and controller handoff had all succeeded. Native
inventory shows `IMG C 26.02.01.0.263`, with the auto-abort timer inactive.
CVK independently verified `26.02.01.0.263.1790107665` through the device path.
The Node is Ready and schedulable, maintenance is Settled, the mutation lease
has no holder and the topology reservation is released.

- Device: `cvk-live/cat9k-lab-101`, 198.51.100.101, serial FOC2520L6H1.
- Source: 17.18.04.0.759.1784396682. Target: 26.02.01.0.263.1790107665.
- Rollout: `catc-auto-reload-20261009-101-r2`.
- Rollout UID: `f7d31c6b-a9b1-4f18-8232-d61982100023`.
- Handoff: `swim-c8004090f9ee96c4008edfea0d947cff`.
- Distribution task: `01a11eff-15f5-7f9a-bb2e-d08498993174` (SUCCESS).
- Activation task: `01a11f1d-1103-7695-af9f-a846d47448db` (SUCCESS).
- Appliance: Catalyst Center 3.2.3-75346.100, normal-reload profile.
- Lab image: `cvk-catc-handoff:20261009-reload3`, Helm revision 186.
- Binary SHA-256: `13ad966cb653776b2e23a415aa31d2acc8ea536033cd0b2effec3d426da51e0d`.

The campaign passed both automatic preparation stages and both controller
inventory synchronizations. Each preparation was a no-op capacity/identity
validation. Unused application archives `trexapp.tar` and `c9kwireshark.tar` were
separately offloaded under supervision, with native SHA-512 verification and
write-ahead dispatch records. Their durable backups are on Ubuntu16 in
`/home/cisco/cvk-lab-backups/20261009-cat9k-lab-101/`. This run must not be
represented as fully unattended remediation of arbitrary application files.

The run exposed and repaired pause/resume grant renewal and native staged-target
observation. It retained immutable staging claims and completed distribution
receipts. Native staged-target validation reuses existing install-add history,
package-state, source-size and device-clock correlation; direct gNOI retirement
semantics are unchanged. Full race regression tests passed on the final build.

The modern activation endpoint with an empty `compatibleFeatures` list was
qualified for this exact appliance build and upgrade path. Native evidence
reports reload reason `Image Install` and xFSU status `Not started yet`.
This is normal-reload qualification, not xFSU qualification or proof of other
appliance builds. Existing workloads on `.100` remained Running/Ready with
zero restarts at completion; all lab CVK workers were Ready.

Distribution performs install-add as well as archive transfer. The archive-only
reserve was insufficient to retain the pre-activation floor after extraction;
the second supervised offload restored 3,108,274,176 bytes free. Future policies
must reserve archive **and extraction** space. Supervised cleanup removed no
OS packages. Catalyst Center subsequently reported its own inactive-image
cleanup after successful commit. Its roughly 65-second clock lead caused
freshness checks to wait; no timestamp validation was disabled.

See `completed-rollout.json`, `completed-upgrade.json`, `completed-handoff.json`,
`native-committed.jsonl`, `native-final-mode.jsonl`, and `release-evidence.json`
for completion evidence. The two sync task trees and readiness runs document
the automatic pre-distribution and pre-activation gates. Offload receipts
document the separate supervised cleanup. The task monitor ended on completion.
