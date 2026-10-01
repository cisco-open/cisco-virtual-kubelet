# Physical topology evidence checkpoint — 1 October 2026

Purpose: preserve the current lab evidence on `pr/johalley/tas-extentions`
for [November roadmap resumption](../../topology-november-handoff.md).
This is **partial qualification**, not a release acceptance certificate.

## Evidence inventory and interpretation

| Saved collection | Original report result | What may be concluded |
| --- | --- | --- |
| [fix1 serial upgrade](fix1-serial-upgrade/report.md) | Historical harness `PASS`, all three devices | Combined software transition to 17.18.03. Later drain review found force-delete/session errors; the historical PASS is not E07 or final candidate acceptance |
| [fix1 serial downgrade](fix1-serial-downgrade/report.md) | Historical harness `PASS`, all three devices | Combined transition to 17.18.02, subject to the same drain/traffic/provenance limits |
| [settled2 `.100` upgrade](settled2-live-upgrade-clean/report.md) | `STOPPED`: ready worker image identity not proven | Device reached 17.18.03. Worker identity gate did not pass; the source directory name `clean` is historical, not a clean-build attestation |
| [settled4 `.100` downgrade](settled4-live-downgrade/report.md) | `STOPPED`: app-log force-delete classifier | Device transition/health results are retained; no blanket retrospective drain pass is assigned |
| [settled5 `.101` downgrade](settled5-lab101-downgrade/report.md) | `STOPPED`: app-log force-delete classifier | Device/rollout success and ordered leaf drain records; offline preceding-log correlation passes, but collector was not rerun and manager/network log files are missing |
| [`11ae6704` `.103` NoReboot boundary](11ae6704-noreboot-boundary.md) | `Failed/ActivationOutcomeUnknown`; later device audit proved 17.18.03 committed and running | OS.Install completed but IOS-XE timed out the NoReboot activation response. CVK correctly retained quarantine. A 17:51 UTC correlated CLI and secure gNOI audit proved the switch had reloaded and committed 17.18.03; this is still not an independently qualified staging boundary. |
| [`.103` C0 outcome audit](c0-103-activation-outcome-audit.md) | Conclusive target-running evidence followed by tested observation-only recovery and healthy settlement | Binds the leaf, device, Node, workers and Lease; records both terminal views, exact candidate/image identity, no-replay recovery, full soak and final fence release. |
| [C2 manager-accepted network evidence](c2-manager-accepted-network-evidence.md) | PASS for the accepted-evidence trust boundary and read-only physical comparison on all three C9Ks | Candidate `90bc690c` separates worker-reported raw samples from manager-accepted evidence, binds acceptance to the current Pod/device/revision, rejects forgery in a native API-server test and survives a manager restart. Loaded-rate accuracy, redundant-supervisor coverage and the full compatibility matrix remain open. |
| [C2 rate-provenance follow-up](c2-rate-provenance-followup.md) | PASS for bounded raw-rate provenance, manager recomputation, sequence/lost-response tests and physical k3s compatibility | Candidate `95077ba7` corrected an OpenAPI integer-bound failure found in the physical lab, converged all six C9K workers, accepted 44–46 rate-qualified interfaces per switch and survived a manager restart. Controlled loaded-path accuracy and reverse compatibility/rollback remain open. |
| [C3 claim-time network authority](c3-claim-time-network-authority.md) | PASS for evidence-bound expiring grants and uncached worker enforcement immediately before each new mutation claim | Candidate `9c48a98c` passed race and real-API qualification, converged the manager and all six C9K workers, and completed secure read-only gNOI Verify on all three switches. Administrator policy, overlapping groups, pacing, continuous recovery/soak and physical service-path scenarios remain open. |

The reports are original historical text, preserved without changing their
conclusions. In particular, historical wording such as “binding observed
before execution” proves only that the collector observed binding; it does
not prove ordering of worker dispatch. Use the interpretation above and the
current execution-plan gaps when citing these records.

[artifact-index.json](artifact-index.json) enumerates **565 source files**,
their original byte counts/SHA-256 values, **469 saved artifacts**, saved
hashes and every omission/transformation. Saved source artifacts total
10,858,918 bytes, excluding the index and checkpoint analysis/docs. No
software image/container archive, kubeconfig, credential or private-key file
is part of this bundle.

- Latest `settled5` JSON, JSONL and full app-worker log are retained, including
  the original empty previous-container log. An empty log proves no events.
- Older collections retain JSON manifests/results/plans/leaves, health,
  worker, event and workload snapshots, and reports. Repetitive historical
  JSONL polling is omitted and listed explicitly in the index.
- Older log files are **filtered excerpts**, identified by `.log.excerpt.txt`;
  original source line numbers are retained. They include error/warning,
  lifecycle, drain, binding, reservation and topology messages. An excerpt
  cannot establish absence of errors, RPCs or traffic loss.
- JSON is normalized; managedFields/last-applied blobs are removed. Session
  tokens are replaced by a SHA-256 correlation marker. Password/auth material,
  URI userinfo and PEM blocks are redacted if encountered. Secret names/UIDs,
  lab addresses, physical identities and operation UIDs are intentionally
  retained for correlation; these files remain lab-specific, not generic examples.

Source locations at capture time: Ubuntu16 `/tmp/cvk-roadmap-next-upgrade2`,
`/tmp/cvk-roadmap-next-downgrade`, `/tmp/cvk-settled4-live-downgrade`; workstation
`/tmp/cvk-settled2-live-upgrade-clean` and `/tmp/cvk-settled5-lab101-downgrade`.
The complete old polling streams/unfiltered historical logs are not durably
archived here; if needed for a new claim, recover them before those temporary
sources expire or rerun the test. Do not infer coverage from source hashes alone.
Earlier fix1 campaigns and the settled3 `.101` upgrade remain historical
execution-plan references, not additional complete runs archived by this bundle.

## Latest `.101` run: primary artifacts

Run: `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`.

- [Manifest before apply](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout.manifest.json)
- [Frozen plan](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout.planning.json)
- [Rollout result](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout.result.json)
- [Leaf / drain evidence](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout.leaf.json)
- [gNOI Verify result](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-verify.result.json)
- [Device show-command health results](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-health.result.json)
- [Full app-worker log](settled5-lab101-downgrade/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout.app-worker.cat9k-lab-101-vk-7d4dd64c4d-5d8kp.log)
- [Final workloads/PDB](settled5-lab101-downgrade/workloads.cat9k-lab-101.downgrade-after.json)
- [Final CiscoDevice/session](settled5-lab101-downgrade/cat9k-lab-101.downgrade.ciscodevice.json)
- [Sampled app-container identity](settled5-lab101-downgrade/cat9k-lab-101.downgrade-final.workers.json)

Frozen approval hash:
`sha256:3a3d70168e146cb47e3d52690879285d81c8e9047f2b1b62283defa2aa79f138`.
Approval principal was `system:admin`: this does **not** qualify least-privilege
roles. Leaf UID: `96509792-b5a8-4283-8d3b-1445ef288c97`.

### Drain correlation, all times UTC on 1 October

| Pod UID | Accepted eviction requested | Deletion observed | Leaf device-clean / inventory revision | Released | Preceding clean acknowledgement / provider success | VK API cleanup marker |
| --- | --- | --- | --- | --- | --- | --- |
| `e717875d-14f8-4355-bbfd-c4fded105336` | 08:08:19 | 08:08:21 | 08:08:40 / 1 (prior revision 0 omitted by JSON) | 08:08:43 | 08:08:43 | 08:09:10 |
| `560bc776-9e4b-47f9-b908-fce5e1018594` | 08:09:12 | 08:09:13 | 08:09:31 / 2 (prior revision 1) | 08:09:32 | 08:09:32 | 08:10:01 |

The full log also contains repeated completion callbacks **after** the markers.
Those later callbacks are not used to justify an earlier delete. Virtual
Kubelet v1.12.0's `deletePodsFromKubernetesHandler` emits this marker when its
API cleanup queue sees the Pod's stale Running status, then performs an
API deletion with a UID precondition. The log line alone does not show a
forced device-side application removal. The predecessor/success records
and leaf timing narrow the interpretation for these two UIDs only.

See [offline-analysis.json](offline-analysis.json) for the saved harness/input
hashes and revalidation result. The classifier is a log-evidence check, not
an authorization mechanism or complete E07 acceptance test. The running
Pod-status cleanup behavior still warrants lifecycle regression coverage.

## Missing evidence and next tests

- No independent application endpoint/forwarding probe, redundant-path,
  congestion, critical-service or routing-adjacency continuity measurement.
- Latest collector aborted before saving manager/network logs; no complete
  final Lease/ledger snapshots. Do not claim absence of unresolved ownership
  solely from `Settled` or a missing taint.
- No full three-device, two-direction run on one clean rebuilt checkpoint
  candidate using the final harness; no independently approved staged activation.
- The `.103` device outcome and exact-identity fence settlement are now
  qualified for candidate `3212f777`; this does not qualify Install-only
  preparation or the broader C2–C9 roadmap.
- Dirty-build source provenance is incomplete. Saved source fingerprints
  identify this checkpoint, not all original image inputs.
- The C2 accepted-evidence boundary now has bound-token forgery, envtest,
  generation, current-binding, concurrent-writer, lost-response and
  manager-restart coverage. Controlled idle/loaded-rate accuracy,
  redundant-supervisor hardware and the reverse old/new compatibility and
  rollback cases remain open. Optional TAS and another hardware platform need their own evidence;
  ordinary three-switch rollouts do not qualify them.

These gaps map to E00–E13 in the [execution plan](../../topology-roadmap-execution.md).
The [handoff](../../topology-november-handoff.md) gives the exact next order.

## Integrity and local verification

From this directory:

```sh
shasum -a 256 -c SHA256SUMS
```

`SHA256SUMS` covers the saved artifacts, index, checkpoint source fingerprint,
offline analysis and verification files (excluding itself). Source hashes in
the index differ from saved hashes when normalization/redaction/filtering
changed the bytes. Git supplies version history; hashes detect accidental
changes but are not signed attestations of the lab.

See [verification.md](verification.md) for local tests and pending gates.
