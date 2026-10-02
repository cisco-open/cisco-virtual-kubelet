# E12 synthetic scale and ownership-fence evidence

Date: 2 October 2026

Final candidate: `1556238a`

This record advances E12-A/B without overstating physical qualification. The
versioned profile is
`internal/topologyrollout/testdata/e12-scale-profile-v1.json`. It fixed the
1/10/50/100-target matrix, 1,000-member synthetic fleet, hard ledger limits,
latency percentiles, memory, API-request, conflict-retry and watch-kind budgets
before the measurements below were run.

## Ownership fence

A retained `Prepared` software leaf is terminal and its manager reservation can
be `Settled`, but its content-addressed receipt deliberately retains exclusive
device/software ownership for later exact activation. The shared authority
check now distinguishes that state from ordinary settled work. Both reverse
writer handoff and managed device deletion fail closed while a managed leaf is
`Prepared` or retains a preparation receipt. No receipt or activation approval
is copied to a new writer. Focused tests prove that the handoff creates no
transition state or replacement access and that deletion remains fenced.

This closes the identified E12-B fail-open path. It does not define receipt
invalidation: external image removal or abandonment remains blocked until a
separate device-reconciled invalidation API is designed and qualified.

## Versioned observability

The manager now exposes:

- `cisco_vk_topology_rollout_reconcile_duration_seconds`, a histogram labeled
  only with the bounded result and reason vocabularies already used by the
  reconciliation counter;
- `cisco_vk_topology_rollout_ledger_conflict_retries_total`, a label-free
  counter incremented only for Kubernetes resourceVersion conflicts;
- the existing active-reservation and serialized-ledger byte gauges.

Controller-runtime/client-go metrics supply process memory, REST request rate
and workqueue/watch behavior. The rollout controller has seven fixed watched
resource kinds; watch cardinality does not grow per device or campaign.

## Reproducible benchmark

Command:

```console
go test ./internal/topologyrollout -run '^$' \
  -bench '^BenchmarkE12Scale' -benchmem -benchtime=1s -count=3
```

The same command was then executed in the exact pinned Linux/arm64 Go 1.26.7
builder image from the repository Dockerfile. Median Linux results were:

| Targets | Reserve + encode + decode | Allocated bytes/op | Ledger bytes | In-process fake-client read | Read bytes/op |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 333.734 µs | 632,512 | 544 | 13.897 µs | 9,565 |
| 10 | 3.360 ms | 6,388,115 | 4,972 | 91.932 µs | 57,772 |
| 50 | 20.245 ms | 33,811,267 | 24,652 | 449.798 µs | 270,291 |
| 100 | 47.222 ms | 72,547,369 | 49,252 | 896.492 µs | 568,551 |

All three repetitions passed. The 100-target ledger is 18.8% of the immutable
256 KiB format ceiling. Existing tests prove a 101st target rejected by the
campaign ceiling leaves all 100 reservations intact, and a 257th active record
or one-byte-over serialized ledger fails closed.

The benchmark allocation total is cumulative transient allocation for building
100 reservations against a repeatedly canonicalized 1,000-member snapshot; it
is not resident memory.

The fake-client column is an encoding/client-cost baseline only. It is not an
API-server latency measurement.

## Real API-server contention and latency

Candidate `12b513b7` adds `TestEnvtest_E12LedgerAPIContentionAndLatency` to the
pinned Kubernetes 1.35 envtest gate. The test uses uncached REST reads against
a real API server and the same versioned 1/10/50/100-target profile. Twenty
reads at each size produced this local run:

| Targets | Ledger bytes | p50 | p95 | p99 |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 570 | 0.328 ms | 0.650 ms | 1.021 ms |
| 10 | 4,998 | 0.367 ms | 0.427 ms | 0.465 ms |
| 50 | 24,678 | 0.705 ms | 1.194 ms | 1.607 ms |
| 100 | 49,278 | 1.092 ms | 1.674 ms | 1.900 ms |

The same test forces two independent `Store.Mutate` calls to read one
ConfigMap resourceVersion before either patch can proceed. One real patch then
receives a Kubernetes conflict; the store reruns the complete mutation against
the fresh ledger and both distinct reservations remain persisted. This proves
the production conflict/revalidation path rather than merely calling it
through a fake client. The p99 check is bounded by the predeclared five-second
reconcile budget. These local envtest timings are regression evidence, not a
managed-control-plane throughput SLO.

## Production-manager sample

The three-device physical manager was sampled before its next rollout. Across
1,367 complete/requeue observations, the bounded histogram established:

- conservative p50 at or below 40 ms;
- conservative p95 at or below 160 ms;
- conservative p99 at or below 1.28 s;
- combined average about 56.3 ms;
- zero ledger-conflict retries;
- resident memory 149,344,256 bytes (about 142.4 MiB).

These values are within the versioned profile's latency, conflict and memory
budgets for this three-device runtime. They do not establish production API
rate or throughput at 100 physical workers; the synthetic 1,000-member/
100-target benchmark remains the large-input evidence.

## Physical single-cluster handoff

Candidate `ae93ca37` migrated an exact historical shared-account owner to the
ownerless steady-state account without adopting foreign RBAC. Candidate
`050ab07a` then admitted only the manager's UID-bound two-step forward
enrollment: the same-name Node had to retain its exact UID and immutable
`topology.cisco.vk/legacy-handoff=<node UID>` marker while managed metadata was
restored and the initialization taint was removed.

On physical `.100`:

1. reverse handoff reached `Complete` with the original Node UID
   `1600578e-9e93-4164-9e47-b0c277a28d5e`;
2. the shared account and RoleBinding became ownerless; temporary UID-scoped
   access and the managed network worker were absent;
3. the exact Node audit marker remained and the Node was Ready;
4. old UID-derived and shared functional identities were denied Node get and
   patch by live authorization checks;
5. forward enrollment reused that exact Node UID, restored the managed
   `NodeIdentity`, removed only the initialization taint and returned both
   functional workers to Ready.

No software mutation, receipt or activation approval was transferred. A new
Prepared receipt created later on `.100` correctly makes another reverse
handoff ineligible until that exact software ownership is resolved.

This passes E12-C for a single physical C9300 within one Kubernetes cluster:
the destination owner was established, old writers were denied, and Node
identity/history were preserved. E12-D remains open because no second cluster
and independently fenced destination credential set were available. Active/
active ownership is still explicitly unsupported.

## Restart, partition and result boundary

The handoff fault suite covers status-before-marker recovery, exact partial-
access revocation, rejection of additive or drifted access, forged recovery
state, rollback after authorization, worker-generation quiescence, unresolved
software/Lease/operation fences and deletion ordering. The physical `.100`
run then proved old-writer denial and same-UID reverse/forward convergence
inside one cluster. A network partition between two independent Kubernetes
clusters remains intentionally unqualified: Kubernetes Leases cannot provide
a shared fence across that boundary, so no test may imply safe active/active
ownership.

E12-A now has the versioned synthetic core benchmark, real API-server read and
conflict behavior, bounded observability and a small-runtime sample. E12-B's
fail-closed software/operation fences and E12-C's physical single-cluster
handoff are complete for the tested path. Cross-cluster E12-D and a production
throughput claim beyond the versioned synthetic envelope remain open
prerequisites rather than inferred passes.
