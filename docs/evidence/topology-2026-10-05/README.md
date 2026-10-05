# Merge-gate checkpoint — 5 October 2026

**Not full-roadmap merge acceptance.** All six required CI jobs passed for
`0067e363c969e86871cde2bdd5973e4fb846b2c8`. PR #197 still requires review.
The physical lab remains on `6f3686e9`; it does not qualify newer recovery code.
No software lifecycle operation, device configuration change, credential change,
ownership transfer or deployment was performed for this checkpoint.

## Exact automated evidence

[GitHub run 37233179342](https://github.com/cisco-open/cisco-virtual-kubelet/actions/runs/37233179342)
completed successfully at `2026-10-04T21:07:54Z` for the full candidate above:

| Job | Start (UTC, 4 October) | Completion (UTC) | Result |
| --- | --- | --- | --- |
| build-and-smoke | 20:43:16 | 21:07:53 | PASS |
| native-tas-conformance | 20:43:16 | 20:46:42 | PASS |
| govulncheck | 20:43:16 | 20:44:15 | PASS |
| helm4-compat | 20:43:16 | 20:43:23 | PASS |
| terraform-provider | 20:43:17 | 20:44:24 | PASS |
| ygot-validate | 20:43:16 | 20:48:42 | PASS |

The native-TAS job's actual assertions include:

```text
2026-10-04T20:46:13.1868627Z native Deployment/ReplicaSet created members,
  survived controller-process restart, and replaced a member with unchanged
  PodGroup identity; synthetic scheduling only
2026-10-04T20:46:13.1920796Z --- PASS: TestNativeTASServedSchedulingGroupGuard (23.88s)
2026-10-04T20:46:32.8564945Z native Kubernetes 1.37 TAS conformance passed:
  site=site-b node=cvk-native-tas-b replacement=cvk-native-tas-b
  maintenance=blocked capacity=blocked scheduler-restart=recovered
```

Line wrapping above is editorial. These results cover E08-A/B's API guard and
disposable scheduling prerequisites, **not** physical application startup,
automatic PodGroup creation, production group drain or service continuity.

## Fresh physical read-only baseline

Ubuntu16 API queries and HTTP probes on 5 October found:

- All three authorized C9K Nodes Ready, reported OS `17.18.3`.
- Both existing app Deployments `1/1`, both Pods `1/1 Running`, zero restarts.
- The existing `minAvailable: 1` PDB allows one disruption.
- Each existing application endpoint returns HTTP 200 from Ubuntu16.
- The historical duplicate Node remains NotReady and excluded. The NX-OS
  Node is not qualified or authorized as a positive lifecycle test target.

Commands were read-only `kubectl get nodes`, `get deployments,pods,pdb` in the
existing lab namespace, plus bounded `curl --fail` against the two existing
application addresses. The raw capture is private:
`/tmp/cvk-20261005-readonly-baseline.log`, SHA-256
`57fff5ece492733d7327dd71a7a0a9d2adffb3a570778196b28b6fbb80f34b39`.
This durable summary is sanitized; the raw log is not a release artifact.
Ready and HTTP snapshots do not establish failover capacity, continuous
forwarding, spare placement or absence of every retained mutation claim.

## Executable acceptance accounting

[`acceptance.json`](acceptance.json) enumerates **all 71** E00–E12 sub-gates and
E13's F01–F13 rows. It is a candidate-specific checkpoint, not a percentage of
implemented features. Historical passes remain in the linked earlier records;
an unqualified carry-forward is deliberately not relabelled a current PASS.
Two directly mapped E08 prerequisites are PASS. Other rows explicitly retain
their pending qualification or incomplete evidence mapping. Group-level
roadmap completion requires every constituent row, not just a green CI job.

Validate the saved checkpoint from the repository root:

```sh
python3 scripts/validate-topology-acceptance.py \
  docs/evidence/topology-2026-10-05/acceptance.json
```

To test full completion, supply the independently obtained intended candidate:

```sh
python3 scripts/validate-topology-acceptance.py \
  docs/evidence/topology-2026-10-05/acceptance.json \
  --candidate 0067e363c969e86871cde2bdd5973e4fb846b2c8 --require-complete
```

**The second command must fail for this checkpoint.** A report for an older
candidate also fails when checked against a newer SHA. Do not advance the
candidate field without collecting or explicitly rerunning the applicable
evidence. Future records should use a new directory and preserve this snapshot.

Each PASS needs exact-candidate runs, commands, timezone-qualified start/end,
zero exit code, zero skipped checks, completed collection, actual observations,
and a durable sanitized artifact under `docs/evidence/` with a verified SHA-256.
Blocked/failed rows need a cause and next action. Duplicate/missing/unknown
gates, unknown fields, duplicate JSON keys, missing/changed artifacts and
paths escaping the evidence directory are rejected. Commands are documentation;
the validator never executes them or accesses the lab.
F13 additionally requires six distinct run `case` values: `c9k-100/upgrade`,
`c9k-100/downgrade`, `c9k-101/upgrade`, `c9k-101/downgrade`,
`c9k-103/upgrade` and `c9k-103/downgrade`. A combined summary or duplicate
direction cannot replace one of those separately recorded executions.

Only E09-B/C/D and F09 can be NOT_APPLICABLE, and only after a current E09-A
PASS plus an explicit measured no-durable-cache decision. Missing hardware,
pending implementation, an interrupted collector or skipped tests cannot be
waived. The validator cannot judge the truth or sufficiency of handwritten
assertions: reviewers must still inspect the referenced tests and evidence.
It is not a merge bot and does not replace GitHub approval, security review or
physical test supervision.

The offline safety suite now runs **all** `scripts/tests/test_*.py` in CI. This
also restores five existing lab-runner safety tests omitted by the previous
single-filename discovery pattern. Local execution passes 36 tests, including
the new ten acceptance-validator tests, with no skips. No runtime or native
admission policy is changed by this accounting increment.

## Required next execution, not optional follow-up

Use [R0–R9 in the execution plan](../../topology-roadmap-execution.md#remaining-completion-plan)
as the controlling work order:

1. **R0/R1:** finish inventory of exact deployed identities, retained ownership
   and calibrated fixtures; qualify interrupted schema/policy/runtime changes,
   reverse-version operation and rollback with retained settled and uncertain
   objects. The old-manager startup rejection is one negative control, not a
   complete migration matrix. Do not deploy a new disruptive candidate first.
2. **R2/R3/R4:** qualify independent forwarding and spare application capacity;
   complete real inactive-image drift/recovery and PDB/service continuity.
   IOS XE's normal stale `InProgress` inventory currently remains held by
   preparation invalidation. Do not weaken this guard based on idle state alone.
3. **R5–R8:** implement the missing opt-in group-drain contract; qualify physical
   native-owner lifecycle, repeated distribution/resource measurements, sustained
   controller scale, a second capable platform and independently fenced offline
   cross-cluster transfer. The three-C9K cohort cannot supply second-platform
   evidence; extra device authority must be explicitly established before use.
4. **R9:** freeze one reproducible candidate and run all six separate
   prepare/hold/approve/activate device/direction sequences with independent
   service/path probes, final ownership settlement, full CI and human review.

Required fixture information remains the calibrated independent data path and
probe endpoints, eligible portable-workload spare capacity, a qualified second
platform/image pair, and independent destination/device-side fencing authority.
There is no authorization here to use CI nodes, disable signing/TLS, reset
ownership, copy approvals or perform an unqualified reload to bypass a blocker.
