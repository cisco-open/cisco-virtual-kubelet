# E12 synthetic scale and ownership-fence evidence

Date: 2 October 2026

Candidate base: `8e09daa2` plus the E12 worktree described here

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

| Targets | Reserve + encode + decode | Allocated bytes/op | Ledger bytes | Uncached-style API read fixture | API-read bytes/op |
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
is not resident memory. The new production histogram/counter and standard
process/client metrics must still be sampled on the real manager to close the
profile's p50/p95/p99, RSS, API-rate and contention budgets. Consequently this
is a reproducible synthetic controller-core result, not full E12-A production
acceptance and not evidence for a fleet larger than the tested profile.
