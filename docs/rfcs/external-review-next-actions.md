# External review next actions

**Branch:** `pr/johalley/ciscoconfig_xe`  
**Date:** 2026-04-25  
**Scope:** recommendations after re-reviewing the post-`latest-update.md` implementation

This note captures the recommended next actions from the latest Codex review. The branch is much stronger than the earlier integration pass, and the following verification is green:

- `helm lint charts/cisco-virtual-kubelet`
- `helm template cvk charts/cisco-virtual-kubelet --namespace cvk-system --set aggregator.enabled=true --set config.leaseNamespace=cvk-system`
- focused package tests for engine, transport, writers, provider, controller, aggregator
- focused race sweep on the updated packages
- `go test -race -count=20 ./internal/drivers/iosxe/configdriver/writers`
- `go test -race -count=20 ./internal/drivers`
- `go test -race -count=5 ./...`

However, the review found five remaining semantic/operational gaps. These should be treated as release-blocking before `latest-update.md` and `implementation-status.md` re-claim day-2 readiness.

## Recommended status change

Temporarily revise the release status from:

> shippable for day-0 AND day-2 under the per-pod topology

to:

> close to day-2 readiness, pending final semantic hardening for transaction atomicity, teardown freshness, lease identity during restarts, prune ownership semantics, and gNMI interface path coverage

Re-claim the stronger status only after the acceptance checks below pass.

## Wave 7A: blocking semantics

### 1. NETCONF CLI transaction semantics

**Finding:** `internal/drivers/iosxe/configdriver/transport/netconf.go:199-207`

The engine now routes CLI template blocks through the transactional transport wrapper, but the NETCONF adapter still special-cases `VerbCLI` by calling `pushCLI` without using the transaction target/handle. During `spec.transactional=true`, structured ops go to candidate while CLI template blocks execute through the Cisco-IA RPC outside candidate semantics. If a later family fails or the transaction is discarded, the CLI changes are not rolled back.

**Preferred fix:**

- Make CLI transaction semantics explicit and fail closed.
- If IOS-XE cannot apply Cisco-IA CLI RPCs to candidate, reject `transactional=true` plus CLI blocks before any writes occur.
- Surface a clear status/error message such as `transactional NETCONF apply does not support CLI template blocks`.
- If IOS-XE does support candidate-bound CLI RPCs, wire that path and prove rollback on discard.

**Acceptance checks:**

- Unit test: transactional NETCONF with a CLI block either rejects before mutation or applies CLI to a transaction-aware path.
- Unit test: structured op + CLI block + later failure leaves no out-of-transaction CLI mutation.
- Documentation states whether CLI templates are atomic under NETCONF transactions.

### 2. configPrereqs teardown freshness gate

**Finding:** `internal/controller/ciscodevice_controller.go:689-699`

The teardown gate checks only `status.phase == InSync`. After the controller updates the owned `IOSXEConfig` to the empty-source teardown spec, the status subresource can still contain the old `InSync` phase from the previous generation. A subsequent CiscoDevice reconcile can delete the CR or remove the finalizer before the per-device reconciler has applied the empty intent.

**Preferred fix:**

- Require `existing.Status.ObservedGeneration == existing.Generation` before treating teardown as complete.
- Keep `existing.Status.Phase == "InSync"` as the second gate.
- Stronger option: also verify `status.lastAppliedHash` matches the canonical hash of the empty teardown intent.

**Acceptance checks:**

- Unit test: old `InSync` status with stale `observedGeneration` does not delete the owned CR.
- Unit test: matching `observedGeneration` plus `InSync` deletes after teardown.
- envtest follow-up: CRD admission and status-subresource behavior are exercised with a real API server.

### 3. Lease identity during restarts and rollouts

**Finding:** `internal/provider/config_reconciler.go:400-404`

Lease ownership is keyed only by `IOSXEConfig` namespace/name. During a per-pod Deployment rollout, including the new credential-secret rotation path, Kubernetes can run old and new pods at the same time. Aggregator worker restarts have a similar overlap window. Because both reconcilers use the same holder identity, both can renew the same lease and write the same device/family concurrently.

**Preferred fix:**

- Add a runtime identity to the lease holder, for example `<namespace>/<iosxeconfig>#<podUID-or-workerUUID>`.
- Per-pod path: inject pod UID with the downward API and pass it into `ConfigReconciler`.
- Aggregator path: generate a unique worker ID per worker start.
- Keep the CR identity separately for status and conflict messages, so operators still see which CR owns the family.
- Consider releasing held leases on shutdown and/or setting Deployment strategy to `Recreate` as a belt-and-suspenders operational mitigation.

**Acceptance checks:**

- Unit test: two reconcilers for the same CR but different runtime identities cannot both acquire the same family lease.
- Unit test: same reconciler/runtime identity renews its lease normally.
- Rollout-oriented test or envtest: credential rotation changes the pod template and the new pod cannot write until the old holder releases or expires.

### 4. Prune semantics and configPrereqs ownership

**Finding:** `internal/drivers/iosxe/configdriver/engine/engine.go:357-367`

The API describes `pruneOnRelinquish` as deleting families removed from `managedFamilies`, but the engine runs `PruneDiff` on every reconcile for every currently managed family whenever the flag is true. Since CiscoDevice configPrereqs sets this flag automatically, a normal day-0 prereq reconcile can delete observed DHCP, ACL, or VPG entries absent from the source, not just entries CVK created during teardown.

**Preferred fix:**

- Do not set `pruneOnRelinquish=true` on steady-state configPrereqs CRs.
- Set it only when the controller intentionally drives the empty-source teardown intent.
- Separately decide whether the public API should be renamed or split:
  - `pruneOnRelinquish`: only deletion/teardown behavior.
  - `authoritativePrune`: continuous whole-family pruning.
- Update API comments and docs so operators understand the ownership model.

**Acceptance checks:**

- Unit test: normal configPrereqs reconcile does not delete unrelated observed entries absent from the source.
- Unit test: teardown empty-source reconcile does produce delete ops for the prereq family set.
- Documentation states whether configPrereqs is additive or authoritative during normal operation.

## Wave 7B: gNMI path coverage

### 5. Add PathSpec to handwritten interface writers

**Finding:** `internal/drivers/iosxe/configdriver/writers/interface_ethernet.go:174-177`

The structured `PathSpec` fix is present for the generic `keyedListWriter`, but handwritten interface writers still emit only string paths. For interface names like `0/0/0`, `opToGNMIPath` falls back to `parseGNMIPath`, which still splits on `/`. The advertised gNMI lab case for `GigabitEthernet=0/0/0` therefore remains unsafe.

**Preferred fix:**

- Add a helper for interface path specs:
  - `/Cisco-IOS-XE-native:native/interface/<type>=<name>`
  - optional child container such as `/switchport`
- Populate `PathSpec` in `ethernetWriter`.
- Populate `PathSpec` in `switchportWriter`.
- Audit all handwritten writers that construct keyed string paths directly. Add `PathSpec` where fallback parsing is not provably safe.

**Acceptance checks:**

- Unit test: `ethernetWriter.Diff` emits `PathSpec` whose last element is `GigabitEthernet` with key `name=0/0/0`.
- Unit test: `switchportWriter.Diff` emits `PathSpec` preserving `name=0/0/1` and the trailing `switchport` element.
- Unit test: gNMI conversion of the writer-produced op preserves slashes in the key.
- Live retest: gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]`.

## Wave 7C: docs and operational runbook

After code fixes land, update the docs in one small pass:

- Revise `latest-update.md` with the actual closure commits for these five findings.
- Re-claim day-2 readiness only after all acceptance checks pass.
- Add explicit operational notes for:
  - NETCONF transactions and CLI template support.
  - configPrereqs normal operation versus teardown behavior.
  - credential Secret rotation rollout behavior.
  - lease holder identity and expected behavior during pod/worker restarts.
  - gNMI interface path support and live-test coverage.

## Suggested sequencing

1. Fix the three P1 lifecycle hazards first: NETCONF CLI transaction behavior, configPrereqs freshness gate, and lease holder identity.
2. Fix prune semantics before any further day-0/day-2 status claim, because it affects operator trust and device ownership boundaries.
3. Finish the gNMI handwritten-writer PathSpec coverage and live retest.
4. Run the full verification sweep again.
5. Update `latest-update.md` and `implementation-status.md` only after the code and tests prove the new status.

## Final verification checklist

- `go test ./internal/drivers/iosxe/configdriver/... ./internal/provider ./internal/controller ./internal/aggregator`
- `go test -race -count=5 ./...`
- `go test -race -count=20 ./internal/drivers/iosxe/configdriver/writers`
- `go test -race -count=20 ./internal/drivers`
- `helm lint charts/cisco-virtual-kubelet`
- `helm template cvk charts/cisco-virtual-kubelet --namespace cvk-system --set aggregator.enabled=true --set config.leaseNamespace=cvk-system`
- live NETCONF transaction test with a structured write and rollback/commit behavior
- live configPrereqs deletion cleanup test
- live gNMI Set test for `GigabitEthernet 0/0/0`
- live credential Secret rotation test in both per-pod and aggregator topologies
