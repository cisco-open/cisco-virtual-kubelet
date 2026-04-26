# Pre-PR evidence bundle — 2026-04-26

This directory captures every gate that ran on the local checkout during the pre-PR test enrichment session described in [`../../pre-pr-test-enrichment-plan.md`](../../pre-pr-test-enrichment-plan.md). Each subdirectory is a category from the plan's §8.

The live-device portions (`03-kind-smoke/`, `04-live-device/`, `05-post-window/`) are intentionally absent in this bundle: §3.7.2 calls for them to come from a maintenance window with lab access and a real Cat9K. They will be added by the operator who runs the runbook.

---

## Branch + commit metadata

| Field | Value |
|---|---|
| Branch | `pr/johalley/ciscoconfig_xe` |
| Base | `main` |
| Commits ahead of `main` | 57 (at the time of capture; will be 58+ after this evidence-bundle commit) |
| Most recent commit on the branch (at capture) | see [`00-environment/snapshot.txt`](./00-environment/snapshot.txt) |
| Helm chart values used | defaults — no overrides |
| Image | not built locally; CI workflow `smoke.yml` builds image fresh per PR |
| Kubernetes binaries used by envtest | k8s 1.30.3 (apiserver + etcd via `setup-envtest use 1.30`) |
| Lab device | not contacted (live tests deferred to operator) |

Full environment dump: [`00-environment/snapshot.txt`](./00-environment/snapshot.txt).

---

## Gate results

| Gate | Result | Evidence |
|---|---|---|
| `go test ./...` (module-wide unit suite) | ✅ PASS, exit 0 | [`01-static-gates/go-test.out`](./01-static-gates/go-test.out) |
| `go test -race -count=5 ./...` (full race detector) | ✅ PASS, exit 0 | [`01-static-gates/go-test-race-count5.out`](./01-static-gates/go-test-race-count5.out) |
| `go vet ./...` | ✅ clean, exit 0 | [`01-static-gates/go-vet.out`](./01-static-gates/go-vet.out) |
| `helm lint charts/cisco-virtual-kubelet` | ✅ 1 chart linted, 0 failed (1 info note about icon) | [`01-static-gates/helm-lint.out`](./01-static-gates/helm-lint.out) |
| CRD / Helm-chart sync (8 of 8) | ✅ in sync | [`01-static-gates/crd-chart-sync.out`](./01-static-gates/crd-chart-sync.out) |
| `make test-envtest` (real-apiserver smokes) | ✅ 3/3 PASS, exit 0 | [`02-envtest/run.out`](./02-envtest/run.out), [`02-envtest/binaries.txt`](./02-envtest/binaries.txt) |
| Kind smoke (`smoke.yml` workflow) | ⏸ deferred to CI run | not present in local bundle; CI artefact will be linked in PR |
| Live-device retests against Cat9K (§6.D.ii of `architectural-review-final.md`) | 🔒 deferred to maintenance window | Operator runs [`../../release-blocker-tests/RUNBOOK.md`](../../release-blocker-tests/RUNBOOK.md); evidence will be saved alongside this directory under `04-live-device/` once each test completes |

---

## What this bundle proves

After this session's commits:

1. **Module-wide build, test, race, vet, lint, and CRD/chart sync all green** — the static gates the pre-PR enrichment plan §6 enumerates.
2. **Real-apiserver acceptance** of the two recurring fake-client blind spots — `LeaseBlocked` admission and underscore-family Lease creation — exercised against a real `kube-apiserver + etcd`. The negative control (writing a bogus phase value gets rejected) confirms the enum is being **enforced**, not just declared.
3. **Three new transport-aware metric counters** — `cisco_vk_config_transactions_total`, `cisco_vk_config_save_startup_total`, `cisco_vk_config_mutate_ops_total` — wired in [`engine.go`](../../../../internal/drivers/iosxe/configdriver/engine/engine.go) with five new unit tests in [`engine/metrics_test.go`](../../../../internal/drivers/iosxe/configdriver/engine/metrics_test.go) (three nil-guard defensives plus two end-to-end wiring assertions on transactional success and discard-on-commit-failure paths).
4. **Stricter live-test verify baseline** — the new [`release-blocker-tests/lib/baseline.sh`](../../release-blocker-tests/lib/baseline.sh) is sourced by every `verify.sh` in tests 01, 02, 04, 05, 06, 07. It enforces `observedGeneration == metadata.generation`, expected phase + Ready condition, no unexpected `ApplyError`, no stale `LeaseBlocked`, no unexpected drift, and metric-counter equalities (per §3 + §5 of the plan).
5. **Test 07 (writeStartup live coverage)** authored end-to-end. It is the new release-blocker test that exercises `Loopback9997` + `writeStartup=true` + `transactional=true` and verifies the `SaveStartupOK` event AND the `cisco_vk_config_save_startup_total{outcome=ok}` counter increments.
6. **Preflight gate** — [`release-blocker-tests/preflight.sh`](../../release-blocker-tests/preflight.sh) — fails the maintenance window before any device mutation if `kubectl` context, device readiness, transport reachability, or operator approval of the test 04 interface is missing.
7. **CI workflow extended** — `.github/workflows/smoke.yml` now runs `go vet`, generated-artefact drift check, CRD/chart sync, `helm lint`, and `make test-envtest` (with a pinned `setup-envtest` version, never `@latest`) before the kind smoke section.
8. **Scripts are committed executable** — `git ls-files --stage` reports mode `100755` on every per-test script and the fetch helper.

---

## What remains for production-readiness

Per the plan §9, three classes of work are intentionally NOT in this bundle:

1. **Kind smoke evidence.** CI's `smoke.yml` produces it on every PR push; this bundle should grow a pointer to the GitHub Actions artefact when the PR is opened.
2. **Live-device retests against the lab Cat9K.** Six tests (`01–07` minus `02`'s no-device-write design); the operator runs them during a maintenance window via the [RUNBOOK](../../release-blocker-tests/RUNBOOK.md) and saves their per-test artefacts (`pre-state.txt`, `apply.out`, `status-before.yaml`, `status-after.yaml`, `events.txt`, `pod-logs.txt`, `metrics-before.txt`, `metrics-after.txt`, `verify.out`, `rollback.out`, `post-state.txt`, `diff.out`) under `04-live-device/<test-id>/`. The §6.D.ii dispositions in [`../../architectural-review-final.md`](../../architectural-review-final.md) flip from `🔒 release blocker` to `✅ verified live` once those land.
3. **Post-window snapshot diff.** A second `fetch-running-config.sh` run at end-of-window, plus a diff against the start-of-window snapshot, demonstrating the device returned to baseline. Belongs under `05-post-window/`.

Once those three are in place this bundle is the production-readiness evidence.

---

## How to read this bundle

- **`00-environment/snapshot.txt`** — `git status`, branch ahead-count, head SHA, Go/kubectl/helm versions, host info. The "what was the test target" record.
- **`01-static-gates/`** — one file per gate, named after the gate. Each ends with an `exit=` line.
- **`02-envtest/run.out`** — verbose `go test -v` output for the three envtest cases. **`02-envtest/binaries.txt`** — version metadata for the apiserver/etcd binaries.
- **`SUMMARY.md`** — this file. Is the index, not authoritative for any single gate; trust the per-file outputs.

A failing bundle for a future PR should look identical in shape to this one but with at least one `exit=` line non-zero or a `🔴` row in the gate-results table. A reviewer scans the table first; if green, drill into the per-file outputs only on suspicion.
