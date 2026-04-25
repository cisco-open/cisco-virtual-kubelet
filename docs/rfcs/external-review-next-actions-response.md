# Response to external next-actions review

**Branch:** `pr/johalley/ciscoconfig_xe`
**Subject of response:** [`external-review-next-actions.md`](external-review-next-actions.md) (Codex, 2026-04-25, post-`latest-update.md`)
**Author:** Josh Halley
**Status:** plan, not implementation. Each finding has a triage verdict, a remediation approach, and an acceptance criterion. The order is the recommended execution sequence; nothing in this document is patched code yet.

This is the third response RFC in the series:

1. [`external-review-response.md`](external-review-response.md) — closed Waves 1–5 against Codex's first review.
2. [`external-review-followup-response.md`](external-review-followup-response.md) — closed Waves 1A-fu through 6B against the follow-up that re-examined the post-Wave-5 state.
3. **This document** — closes Wave 7A and 7B against the next-actions review that re-examined the post-`latest-update.md` state.

---

## 1. Bottom-line response

The next-actions review is **accurate**. I spot-checked every cited file:line — every claim verifies against the post-FU code. Nothing is contested.

Two findings are subtle race/lifecycle hazards that the post-FU verification missed:

- **Finding #2 (configPrereqs freshness gate)** — the teardown's `Status.Phase == "InSync"` gate can pass on stale status from a prior generation, *before* the per-device reconciler has applied the empty intent. The Wave 4A-fu unit test set `Phase=InSync` directly with `r.Update`, so it never hit the staleness window. A real per-device reconciler updating its status asynchronously would.
- **Finding #3 (lease identity during rollouts)** — Wave 6B's credential-rotation rollout creates exactly the overlap window this finding describes: a new pod stamped with a fresh annotation comes up *while the old pod is still running*. Both pods have the same lease identity (`namespace/name`), both can renew the lease, both can write the same `(device, family)` concurrently. Wave 6B's rotation fix is *active dangerous* without a runtime identity.

The other three findings are correctness gaps:

- **#1 NETCONF CLI in tx** — Wave 1A-fu routed CLI through `applyTransport` at the engine level, but the NETCONF transport's `Mutate` special-cases `VerbCLI` and hands it to `pushCLI` regardless of the tx target. CLI ops always write running.
- **#4 prune semantics** — Wave 4A-fu set `PruneOnRelinquish=true` on the steady-state configPrereqs upsert. Combined with the engine running `PruneDiff` per-family every reconcile, this means a normal day-0 prereqs apply will *delete* any device-side entries the source doesn't list. The API doc says "families removed from `managedFamilies`"; the implementation does "every entry in every managed family". They disagree.
- **#5 PathSpec handwritten writers** — Wave 5A-fu wired PathSpec on the shared `keyedListWriter`. `interface_ethernet.go` and `interface_switchport.go` are handwritten, not via `keyedListWriter`, and still emit string-only paths. The `parseGNMIPath` fallback splits on `/`, so the lab case `GigabitEthernet=0/0/0` is the very thing Wave 5A-fu was supposed to fix and didn't.

The reviewer's recommendation — **walk back day-2 readiness until these close** — is correct. [`implementation-status.md`](implementation-status.md) §1 currently re-claims day-2; that walk-back is part of this plan's Wave 0-na (next-actions).

The reviewer's bottom line — *"close to day-2 readiness, pending final semantic hardening"* — is the operating principle of this plan. Closing #1–#5 closes it.

---

## 2. Per-finding triage

Status column: **confirmed** = I read the cited code and the finding is accurate.

| # | Severity | Title | Status | Spot-check |
|---|---|---|---|---|
| NA-1 | P1 | NETCONF CLI bypasses transaction (always writes running) | confirmed | `netconf.go:204-209` `pushCLI` is called regardless of `target`; the `target` variable feeds only `editConfigXML` |
| NA-2 | P1 | configPrereqs teardown `Status.Phase` gate is stale-status-vulnerable | confirmed | `ciscodevice_controller.go:660-664` checks only `existing.Status.Phase != "InSync"`; no `ObservedGeneration` comparison |
| NA-3 | P1 | Lease identity is per-CR; old/new pod overlap during rollouts both renew | confirmed | `config_reconciler.go:400` `identity := cr.Namespace + "/" + cr.Name` |
| NA-4 | P2 | `pruneOnRelinquish=true` is set on steady-state prereqs CR; engine runs `PruneDiff` every tick → continuous authoritative pruning | confirmed | `ciscodevice_controller.go:728` upsert path sets it unconditionally; `engine.go:357-367` runs `PruneDiff` for every managed family every tick when the flag is true |
| NA-5 | P2 | `PathSpec` absent on handwritten interface writers; gNMI fallback splits `GigabitEthernet=0/0/0` | confirmed | `interface_ethernet.go:174-177` and `interface_switchport.go` emit `transport.Op{Path: ...}` only |

Nothing is **disputed**.

---

## 3. Remediation plan

Two waves; ~8–12 engineer-days total. Wave 7A is the four blocking semantics; ship the P1s first then the P2 prune semantics. Wave 7B is the gNMI handwritten-writer coverage. Wave 7C is the docs sweep.

### Wave 0-na — status walk-back (immediate, ~0.25 ed)

[`implementation-status.md`](implementation-status.md) §1 currently says "shippable for day-0 AND day-2 under the per-pod topology". Walk back to the reviewer's recommended phrasing:

> close to day-2 readiness, pending final semantic hardening for transaction atomicity, teardown freshness, lease identity during restarts, prune ownership semantics, and gNMI interface path coverage

Re-claim "day-2 shippable" only when every Wave 7A and 7B item closes with passing tests AND the reviewer's verification checklist runs green.

This walk-back lands as a separate, immediate commit before any Wave 7 code change starts.

### Wave 7A.1 — NETCONF transactional+CLI rejection (P1, ~1 ed)

**What.** `netconfTransport.Mutate` (`netconf.go:199-221`) computes `target` from the `tx` handle for `editConfigXML`, then ignores `target` entirely when the verb is `VerbCLI` and unconditionally calls `pushCLI` — which routes through Cisco-IA `cli-config-data`, an RPC that operates directly on running config by design. Under `spec.transactional=true`, structured ops land in candidate while CLI ops land in running. If a later structured op fails and the engine `Discard`s, the CLI changes already happened to running.

**Approach.** Decision-point: do we make CLI atomic with NETCONF candidate, or do we reject the combination?

- **Option A — reject.** Cisco-IA `cli-config-data` is documented as a CLI-execution channel; there is no candidate-bound variant of the same RPC. Wiring it to candidate would require a different mechanism (e.g., the `clish` data-model mapped through `<edit-config>`), which has its own correctness pitfalls and isn't currently supported by this branch's NETCONF adapter. **Reject loudly, fail-closed:** when `spec.transactional=true` AND the resolved intent has any `CLIBlocks`, the engine returns `Phase=Failed` with `Err=ErrTransactionalCLIUnsupported` *before* any mutation runs. Operators see a clear status and a Warning event.
- **Option B — wire candidate-bound CLI.** Substantial transport-level work. Out of scope for closing the immediate hazard.

**Recommend Option A.** Fail-closed is safe today; Option B can land later as an enhancement if Cisco-IA gains a candidate-bound CLI path.

**Acceptance.**
- Unit test (engine): resolved intent with `Transactional=true && len(CLIBlocks)>0` → `Phase=Failed`, no Mutate calls on the transport, the Discard path is invoked if a transaction had been opened.
- Unit test (reconciler): the Failed phase surfaces a `TransactionalCLIUnsupported` Reason on the Ready condition.
- Documentation in [`docs/CONFIGURATION.md`](../CONFIGURATION.md): explicit "transactional NETCONF apply does not support CLI template blocks" note.
- Existing transactional + non-CLI tests stay green.

### Wave 7A.2 — configPrereqs teardown freshness gate (P1, ~1 ed)

**What.** `reconcileConfigPrereqs` step 2 (`ciscodevice_controller.go:660-664`) checks only `existing.Status.Phase != "InSync"`. Status writes are asynchronous; a previous reconcile's `Phase=InSync` value can remain after the controller mutates `spec.source.inline` to empty. The gate then passes incorrectly, and step 3 deletes the owned CR before the per-device reconciler has applied the empty intent. Device state is left behind.

**Approach.** Strengthen the gate to require:

1. `existing.Status.Phase == "InSync"`, AND
2. `existing.Status.ObservedGeneration == existing.Generation` — the per-device reconciler has SEEN and ACTED on the post-update spec.

Optionally add: `existing.Status.LastAppliedHash == intent.CanonicalHash(emptySource)` as a third gate. The two-of-three gate is sufficient; the third is defensive.

The Wave 4A-fu unit test passed because it `r.Update`d `Status.Phase=InSync` directly. The replacement test asserts that:
- Stale `InSync` with `ObservedGeneration < Generation` → teardown waits.
- Matching generations + `InSync` → teardown deletes.

**Acceptance.**
- Unit test: stale `InSync` + lower `ObservedGeneration` → `teardownComplete=false`.
- Unit test: matching generations + `InSync` → `teardownComplete=true` and CR deleted.
- envtest follow-up tracked separately (the FU-2 lesson: schema validation + status subresource behaviour need a real apiserver to exercise correctly).

### Wave 7A.3 — Lease holder runtime identity (P1, ~2 ed)

**What.** `config_reconciler.go:400` derives lease identity as `cr.Namespace + "/" + cr.Name`. Two reconcilers running for the same CR (old pod + new pod during a Deployment rollout, two aggregator workers during a manager restart) share the same identity and both succeed at `lease.Renew`. Both can then write the same `(device, family)` concurrently — the duplicate-writer hazard the lease was meant to prevent.

Wave 6B (credential rotation) made this **acutely** dangerous: rotating a Secret rolls the per-device pod, and during the rolling-update window both pods write the device.

**Approach.** Add a runtime identity to the lease holder string.

- **Per-pod path.** Inject pod UID via downward API (`spec.nodeName`/`metadata.uid` field-ref env var). The cisco-vk binary reads it at startup and threads it into `ConfigReconciler.RuntimeID`. Identity becomes `<namespace>/<name>#<podUID>`.
- **Aggregator path.** Generate one `uuid.NewString()` per worker start and pass into the per-device `ConfigReconciler.RuntimeID`. Identity becomes `<namespace>/<name>#<workerUUID>`.
- **CR identity for status messages.** Keep the `<namespace>/<name>` form for the `Conflict` condition's "owned by" text — operators want to see the CR, not the pod UID, in human-readable status.
- **Pod restart belt-and-suspenders.** Set `Deployment.spec.strategy.type=Recreate` for per-device VK Deployments. This is a small operational change; the trade-off is a brief downtime during rollout in exchange for guaranteed no-overlap. Recommend Recreate for VK pods (apphosting tolerates it) but flag it as a Helm value override (`vk.deploymentStrategy: Recreate|RollingUpdate`) so operators can choose.

**Acceptance.**
- Unit test: two `ConfigReconciler` instances with the same CR identity but different RuntimeIDs cannot both `Acquire` the same family lease. The first acquires; the second sees `Owned=false, Holder=<first identity>`.
- Unit test: same identity (renewal of own lease) succeeds.
- Live rollout test: trigger a credential-secret rotation against the lab Cat9K; observe that the new pod waits for the old pod's lease to expire (or for the Recreate strategy to terminate the old pod first). No two pods write the device concurrently.

### Wave 7A.4 — `pruneOnRelinquish` semantics fix (P2, ~1 ed)

**What.** `ciscodevice_controller.go:728` (Wave 4A-fu) sets `PruneOnRelinquish=true` on the upsert path of the controller-owned configPrereqs CR. The engine runs `PruneDiff` for every managed family on every reconcile when the flag is true. Net effect: a normal day-0 prereqs apply silently *deletes* any device-side entry in those families that isn't in `source.inline` — even entries the operator added through other channels.

The API field's docstring says "families *removed from `managedFamilies`* are deleted from the device on the next reconcile". The implementation does "every entry in every still-managed family is pruned". They disagree, and the implementation matches the more aggressive of the two readings.

**Approach.** Resolve the disagreement at the controller-owned CR level by NOT setting the flag on steady-state. Two parts:

1. **Controller-owned CR upsert** (the fix that closes the immediate operator-visible hazard): set `PruneOnRelinquish=true` only when entering the teardown shape (Wave 4A-fu's step 1). Steady-state upsert leaves it `false`. Normal prereqs reconcile is then additive — operator-added entries are preserved.
2. **API documentation**: the docstring on `IOSXEConfigSpec.PruneOnRelinquish` already says "families removed from `managedFamilies`" — that's the intended semantics, but the implementation does continuous pruning. Either fix the implementation or fix the docstring; we choose the docstring honesty path:
   - Update the docstring to: "When true AND `managedFamilies` is non-empty, the engine continuously prunes entries on the device that are not in the resolved intent for those families. The CiscoDevice controller toggles this only during configPrereqs teardown; user-authored IOSXEConfig CRs may set it explicitly to opt into authoritative pruning."
   - Optionally add a future API field `authoritativePrune` for the continuous case to disambiguate from the relinquish-only original intent. Defer the rename to v1 promotion (`crd-v1-promotion-plan.md` already lists shape changes for v1).

**Acceptance.**
- Unit test: normal configPrereqs upsert produces an owned CR with `PruneOnRelinquish=false`.
- Unit test: existing teardown test still asserts `PruneOnRelinquish=true` during the empty-source step.
- Unit test: a user-authored IOSXEConfig CR with `PruneOnRelinquish=true` continues to behave as before (continuous prune; this is opt-in for power users).
- Live retest (operator-scheduled): apply a CiscoDevice with prereqs that bring up a VPG, then manually configure an unrelated DHCP pool on the device, then re-trigger a prereqs reconcile. Assert the unrelated DHCP pool is preserved.

### Wave 7B — PathSpec on handwritten interface writers (P2, ~2 ed)

**What.** Wave 5A-fu added structured `PathSpec` on the shared `keyedListWriter`. `interface_ethernet.go` and `interface_switchport.go` are handwritten — they don't go through `keyedListWriter`. They emit `transport.Op{Path: yangPath + "=" + name}` only. For `name="0/0/0"` the gNMI transport's fallback `parseGNMIPath` splits on `/` and produces three wrong path elements.

The handwritten writers are the ones the reviewer's lab case (`GigabitEthernet=0/0/0`) actually exercises. Wave 5A-fu fixed the wrong code path.

**Approach.** Two helpers + audit.

1. **`pathSpecForInterface(yangPath, name string) []transport.PathElement`** — the simple case for interface writers without a child container. The PathSpec walks `/Cisco-IOS-XE-native:native/interface/<Type>` and attaches `Keys{"name": name}` on the final segment.
2. **`pathSpecForInterfaceChild(yangPath, name, child string) []transport.PathElement`** — for `interface_switchport` which tacks `/switchport` after the keyed segment.
3. **Audit pass.** `grep -nrE 'Path: .* "=" \+' internal/drivers/iosxe/configdriver/writers/` to find every other handwritten writer that builds keyed string paths directly. Add `PathSpec` everywhere fallback parsing isn't provably safe.

**Acceptance.**
- Unit test: `ethernetWriter.Diff` emits `PathSpec` whose last element is `GigabitEthernet` with key `name=0/0/0`.
- Unit test: `switchportWriter.Diff` emits `PathSpec` preserving `name=0/0/1` and the trailing `switchport` element.
- Unit test: gNMI conversion of the writer-produced op preserves slashes in the key (the `TestOpToGNMIPath_PathSpecHandlesSlashInKey` pattern from Wave 5A-fu generalised over the new writers).
- Live retest (operator-scheduled): gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]` against the lab Cat9K with gNMI/6030 enabled.

### Wave 7C — docs and operational runbook (~1 ed)

After Wave 7A and 7B land:

- Update [`latest-update.md`](latest-update.md) with the per-finding closure commits and any new architectural lessons.
- Re-claim day-2 readiness in [`implementation-status.md`](implementation-status.md) §1 only after the verification checklist runs green.
- Add operational notes to [`docs/CONFIGURATION.md`](../CONFIGURATION.md) and [`docs/troubleshooting.md`](../troubleshooting.md):
  - Transactional NETCONF + CLI templates: not supported, fail-fast.
  - configPrereqs normal operation: additive (steady-state); authoritative only during teardown.
  - Credential rotation rollout: pod template annotation roll → ReplicaSet rolls → old pod terminated → new pod takes lease. Recreate strategy recommended for clean lease hand-off.
  - Lease holder identity: `<ns>/<name>#<podUID|workerUUID>`. Operators see CR identity in status; the runtime suffix lives in the lease.
  - gNMI interface paths: structured `PathSpec` on every keyed-list writer.

---

## 4. Sequencing and effort

Total estimate: **~8–12 engineer-days**.

| Wave | Scope | Engineer-days | Severity |
|---|---|---|---|
| 0-na | Status walk-back | ~0.25 | required first |
| 7A.1 | NETCONF transactional+CLI rejection | ~1 | P1 |
| 7A.2 | configPrereqs teardown freshness gate | ~1 | P1 |
| 7A.3 | Lease holder runtime identity | ~2 | P1 (acutely worsened by Wave 6B) |
| 7A.4 | `pruneOnRelinquish` only during teardown | ~1 | P2 (data-loss surface) |
| 7B | PathSpec on handwritten interface writers | ~2 | P2 |
| 7C | Docs + status re-claim | ~1 | required last |

Recommended execution order:

1. **Wave 0-na walk-back** (~0.25d, immediate, single PR).
2. **Wave 7A.3 lease identity FIRST** — it's the one finding that's *actively dangerous* in the post-Wave-6B state. Closing it first removes the duplicate-writer hazard credential rotation now exposes.
3. **Wave 7A.1 NETCONF tx+CLI** + **7A.2 freshness gate** in parallel — independent surfaces, both P1.
4. **Wave 7A.4 prune semantics** — operator-trust fix.
5. **Wave 7B handwritten writer PathSpec** — independent, can run in parallel with anything.
6. **Wave 7C docs and re-claim** after every code change above is in.

---

## 5. What "shippable for day-2" means after THIS plan

The acceptance criteria from previous response RFCs remain in force; this plan adds five more:

1. NETCONF transactional + CLI block combination produces a clean `Phase=Failed` with no device-side mutation, surfaced as a clear operator-visible Reason.
2. configPrereqs teardown waits for the per-device reconciler to observe and act on the post-update spec before deleting the owned CR.
3. Pod rollout (credential rotation, image change) does not produce a duplicate-writer window. Two reconcilers with the same CR identity but different runtime IDs cannot both hold the lease.
4. Normal day-0 prereqs reconcile is additive — operator-added device-side entries in the prereq families are preserved across reconciles.
5. gNMI Set against `GigabitEthernet=0/0/0` (and the matching switchport path) lands on the device with the correct keyed path.

Until all five close, [`implementation-status.md`](implementation-status.md) §1's day-2 claim stays walked back.

---

## 6. What this plan does NOT address

Out of scope, intentionally:

- **CRD v1 promotion.** Tracked in [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md). The `pruneOnRelinquish` rename to `authoritativePrune` (or whatever the v1 cut decides) belongs to v1 conversion-webhook PR, not to this plan.
- **Log unification.** Tracked in [`log-unification-plan.md`](log-unification-plan.md).
- **Phase-8 external residuals.** Tracked in [`phase-8-residuals.md`](phase-8-residuals.md).
- **Cisco-IA candidate-bound CLI RPC** (Wave 7A.1 Option B). Defer until Cisco-IA exposes a candidate-bound mechanism; today's IOS-XE does not.
- **envtest infrastructure for schema-validating tests** (FU-2's leftover). Independent piece of work; the immediate Wave 7A.2 fix is the gate logic, not the test infrastructure around it.

---

## 7. Process notes

- This document is a **plan** at the point of authoring. Each Wave is intended to be a separate PR/commit, reviewable on its own.
- The status-doc walk-back (Wave 0-na) lands as a single small commit alongside this RFC, before any code change starts.
- The reviewer's specific verification checklist (review §"Final verification checklist") is added to the post-Wave 7C verification gate.
- Two architectural lessons from this round to internalise in commit messages and inline comments:
  - **Async status subresources mean `Status.X` and `Spec.Y` can disagree** during a reconcile cycle. Gates that read both must explicitly verify they refer to the same generation.
  - **A lease that protects against in-process duplicate writers does NOT protect against cross-process overlap during pod/worker rollouts** unless the holder identity is process-unique.

The reviewer's bottom line — *"close to day-2 readiness, pending final semantic hardening"* — is the operating principle of this plan. Closing #1–#5 closes it.
