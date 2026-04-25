# Response to external follow-up review

**Branch:** `pr/johalley/ciscoconfig_xe`
**Subject of response:** [`external-review-followup.md`](external-review-followup.md) (Codex, 2026-04-25, post-fix)
**Author:** Josh Halley
**Status:** plan, not implementation. Each finding has a triage verdict, a remediation approach, and an acceptance criterion. The order is the recommended execution sequence; nothing in this document is patched code yet.

This is the second response RFC in this series. The first
([`external-review-response.md`](external-review-response.md)) closed
the Wave-1..Wave-5 remediation against Codex's initial review. The
follow-up review re-examined the post-fix state and identified five
gaps where the fixes were either too shallow, made via a path the
production binary never executes, or in one case (configPrereqs)
both schema- and engine-rejected.

---

## 1. Bottom-line response

The follow-up is **accurate**. I spot-checked every claim against
the post-fix code; nothing is contested. The most consequential
finding is P1 #2 (configPrereqs teardown sets `ManagedFamilies=nil`
which the CRD's `MinItems=1` and the engine's empty-list reject
both block) — my Wave 4A unit test passed only because the fake
client doesn't enforce CRD schema validation. Against a real API
server the teardown path would never have worked.

The second-most consequential is P2 #4 (gNMI key registration via
`schema.LoadFamilies` side-effect) — production startup uses
`iosxebuilder.KeyRulesForXE()` and `LoadYANGReleaseTags` (which
calls `schema.LoadYANGReleases`, not `LoadFamilies`); only
`tools/cisco-vk-config-docs` actually calls LoadFamilies in the
codebase. Wave 5A's registry-population was therefore a no-op in
the running binary. Tests passed because tests called LoadFamilies
explicitly.

[`implementation-status.md`](implementation-status.md) §1's "shippable
for day-2" claim must walk back again. The walk-back is part of
this plan in §3 below.

The reviewer's bottom-line — *"the implementation is substantially
improved, but not complete"* — is the correct verdict. Day-2 stays
not-shippable until the five items below close.

The first response RFC's pattern (acknowledge, triage, sequence
into waves, ship in order, verify with tests) is the operating
principle here too. The labelling continues from Wave 5 — the new
follow-up waves are Wave 6A and 6B, plus follow-ups within Waves
1A, 4A, and 5A.

---

## 2. Per-finding triage

Status column: **confirmed** = I read the cited code and the
finding is accurate; **accepted** = the finding is architecturally
correct and not contested.

| # | Severity | Title | Status | Spot-check |
|---|---|---|---|---|
| FU-1 | P1 | NETCONF transactional verify reads `running`; CLI template bypasses tx | confirmed | `transactional_view.go:63-65` delegates Fetch unchanged; `netconf.go:141` hard-codes `<source><running/>`; `engine.go:506` calls `e.Transport.Mutate` directly |
| FU-2 | P1 | configPrereqs teardown sets `ManagedFamilies=nil`, rejected by both CRD MinItems=1 and engine validate() | confirmed | `iosxeconfig_types.go:78` `+kubebuilder:validation:MinItems=1`; `engine.go:310` `errors.New("ManagedFamilies is empty")` |
| FU-3 | P2 | Subscribe notify unused in per-pod controller-runtime path | confirmed | `config_reconciler_controller.go` Reconcile/SetupWithManager never reads `r.SubscribeNotify`; only `Run` does |
| FU-4 | P2 | gNMI keyed paths split key values containing `/`; production never registers keys | confirmed | `parseGNMIPath` splits on `/` before parsing keyed values; production startup uses `KeyRulesForXE` and `LoadYANGReleaseTags`, never `LoadFamilies` |
| FU-5 | P2 | CiscoDevice `credentialSecretRef` rotation not reconciled | confirmed | `ciscodevice_controller.go:310-318` injects via env valueFrom (no pod restart on Secret change); aggregator `specHash` records existence not value |

Nothing is **disputed**.

---

## 3. Remediation plan

Five waves; ~12–17 engineer-days total. Wave 1A-fu and Wave 4A-fu
are P1 and gate any future "day-2 shippable" claim. Wave 6A is the
last advertised drift fast-path (gNMI Subscribe) that doesn't yet
work in the default topology. Wave 5A-fu is the largest single
chunk and the only one that requires a real transport-API change.
Wave 6B is the day-2 secret-rotation hygiene the aggregator and
per-pod paths both need.

### Wave 0-fu — status walk-back (immediate, ~0.25 ed)

`implementation-status.md` §1 currently says "shippable for day-2".
Walk back to the pre-Wave-1A state, with a concrete pointer to this
RFC for the gating set. §5 live-verification table grows new rows
acknowledging:

- transactional NETCONF verify is broken even though Wave 1A
  unit-tested success;
- configPrereqs teardown is broken even though Wave 4A
  unit-tested success (test passed against a fake client);
- gNMI registry never fires in production;
- per-pod Subscribe fast-path is unconsumed;
- credential Secret rotation is not reconciled.

This walk-back is a separate, immediate commit; it goes in **before**
any of the FU waves start so anyone consulting the status doc gets
the corrected view.

### Wave 1A-fu — transactional verify + CLI in tx scope (~3 ed)

**What.** Two related bugs in the transactional apply path:

1. `transactionalView.Fetch` delegates to the inner transport
   unchanged. NETCONF's `Fetch` reads the running datastore. After
   editing the candidate, the verify-Fetch sees the *old* running
   state, the verify-Diff reports residual drift, and the engine
   marks the tick failed and discards the candidate. The
   transaction therefore never commits a successful apply.
2. `applyCLIBlock` calls `e.Transport.Mutate(ctx, "", ...)` directly
   instead of through `e.applyTransport`. CLI ops always write
   running config, even mid-transaction. The atomicity guarantee
   the transaction was supposed to provide doesn't extend to CLI.

**Approach.**

For (1), introduce an optional `TxFetcher` interface in the
transport package:

```go
// TxFetcher is an optional capability: a transport that can read
// from a specific transaction's working datastore (NETCONF
// candidate, gNMI staged set buffer) implements it. transports
// without working-datastore semantics simply don't implement it
// and the engine reads through Fetch (running) as today.
type TxFetcher interface {
    FetchTx(ctx context.Context, tx TxHandle, path string) ([]byte, error)
}
```

`transactionalView.Fetch` then prefers `inner.FetchTx(ctx, v.tx, path)`
when `inner` implements `TxFetcher`, falling back to plain `Fetch`
otherwise. The NETCONF transport implements `FetchTx` with
`<get-config><source><candidate/></source>`. RESTCONF and gNMI keep
their existing `Fetch` (running/state) since they have no
candidate datastore equivalent that's a meaningful read source
during apply.

For (2), route `applyCLIBlock` through `e.applyTransport` instead
of `e.Transport`. CLI ops then participate in the transaction
under NETCONF (cisco-ia:cli-config-data inside the candidate edit)
and remain on running for non-transactional ticks. Cisco's NETCONF
RPC `cisco-ia:cli-config-data` already works with target=candidate;
the existing `Mutate` impl threads the target correctly.

**Acceptance.**

- New unit test: NETCONF stub implementing `TxFetcher` returns the
  candidate-shaped response; verify-Diff reports clean → tick
  reaches `Commit`.
- New unit test: `applyCLIBlock` under transactional mode hits the
  view's `Mutate`, NOT the bare transport's. Use the existing
  `txTransport` mock with mutateHandlesSeen.
- Existing transactional tests stay green.
- Live retest (operator-scheduled, modifies device) of
  `spec.transactional=true` against the lab Cat9K via NETCONF/830
  with at least one CLI template block in the resolved intent —
  assert that running shows ALL writes after commit, none mid-
  transaction.

**Effort.** ~2 engineer-days code, ~0.5 day tests, ~0.5 day live
retest.

### Wave 4A-fu — configPrereqs teardown that the engine accepts (~2 ed)

**What.** Wave 4A's teardown set `ManagedFamilies=nil` and `source.inline=nil`,
intending the engine to run no families and prune device state via
`pruneOnRelinquish`. Two distinct rejections block this:

1. The CRD validates `+kubebuilder:validation:MinItems=1` on
   `managedFamilies`. A real API server rejects the update at
   admission. (Fake test client skipped this validation; the
   Wave 4A unit test passed for the wrong reason.)
2. The engine's `validate()` returns `errors.New("ManagedFamilies
   is empty")` for a zero-length list, so even if admission
   accepted the update the engine would set `Phase=Failed` and
   never advance to InSync — the deletion finalizer would wait
   forever.

`pruneOnRelinquish` was also misread: it triggers the prune *for
managed families* (every tick prunes-via-PruneDiff inside the
per-family loop). It does NOT trigger a special "delete every
family that left ManagedFamilies" pass. With ManagedFamilies=nil
the engine has nothing to prune.

**Approach.**

Replace the teardown shape. Instead of clearing ManagedFamilies,
*keep* the prereq family list (the constant `apphostingPrereqFamilies`)
and set `source.inline` to an empty body (`{}`). The engine then:

1. Iterates `apphostingPrereqFamilies` as ManagedFamilies.
2. For each family, fetches device state, diffs against empty
   desired (no add/update ops).
3. With `pruneOnRelinquish=true` and a `PruneCapable` writer,
   `PruneDiff(empty, observed)` returns DELETE ops for every
   entry currently on the device.
4. Engine applies the deletes, verify-fetches empty, marks the
   family InSync.
5. After all families are InSync, status.phase flips to InSync
   and the controller's teardown step 3 deletes the CR.

Pre-flight check: every family in `apphostingPrereqFamilies` must
have a working `PruneCapable` implementation. Audit needed:

| Family | Writer | PruneCapable? |
|---|---|---|
| `interface_virtual_port_group` | (interface_virtual_port_group.go) | TBD |
| `dhcp` | dhcp.go | TBD |
| `access_list_extended` | access_list_extended.go (already uses keyed_list with prune) | TBD |

If any family does NOT implement PruneCapable, the controller
should record an explicit `PrereqFamilyNotPrunable` event and
either:
- (a) fail the teardown and surface the limitation to the operator, or
- (b) delete the owned CR anyway with a warning that device-side
  config for that family will be left.

The shape of the API contract documentation needs to match what
gets shipped. Either way, the empty-ManagedFamilies path goes away.

**Acceptance.**

- Unit test: teardown step 1 produces an owned CR with
  `len(ManagedFamilies)>0` and `Source.Inline=={}` and
  `PruneOnRelinquish=true`.
- Unit test: with a fake engine reaching InSync on the empty-intent
  reconcile, teardown step 3 deletes the CR.
- envtest-style or controller-runtime integration test exercising
  the schema validation — the unit suite alone is not enough since
  fake.Client skips MinItems. (This was the lesson of FU-2.)
- Audit + table-test: each family in `apphostingPrereqFamilies`
  declares whether it implements PruneCapable; the controller's
  teardown rejects/warns on non-prunable families.
- Live retest (operator-scheduled): apply a CiscoDevice with
  configPrereqs that bring up VPG + DHCP, delete the CiscoDevice,
  assert the device's `show running-config` shows no leftover VPG
  or DHCP pool entries the controller created.

**Effort.** ~1.5 engineer-days code, ~0.5 day tests, ~0.5 day
audit + live retest.

### Wave 6A — Subscribe fast path in per-pod controller-runtime (~1.5 ed)

**What.** `cmd/cisco-vk/config_reconciler.go` starts a Subscribe
watcher and sets `r.SubscribeNotify` on the
`provider.ConfigReconciler`. The polling `Run` method consumes the
channel; `SetupWithManager`/`Reconcile` (the production
controller-runtime path) does not. Only the aggregator and the
legacy polling path benefit from the fast-path; the default per-pod
production topology still waits for the next periodic
`driftDetectInterval` tick to detect out-of-band changes.

**Approach.**

Two viable shapes; pick one:

- **Option A (recommended).** Replace the in-pod manager's
  channel-set-on-reconciler pattern with a controller-runtime
  `source.Channel` event source. The Subscribe watcher writes
  `event.GenericEvent` values to the source's channel; the
  manager's controller picks them up and enqueues reconcile
  requests for all IOSXEConfigs targeting this device. This is the
  standard controller-runtime idiom for external-stream events and
  has the side-benefit of getting predicate filtering and
  workqueue rate-limiting "for free".
- **Option B.** Run a small goroutine alongside the manager that
  reads the existing notify channel and explicitly calls
  `mgr.GetClient().List(...)` + enqueues per-CR reconcile requests
  via the controller's workqueue. More glue code, but no event-
  source plumbing.

Recommend **Option A** — it's the idiomatic pattern and the small
dependency on a `source.Channel` is fine.

The reconcile-trigger plumbing is already in place from Wave 1B —
when the controller-runtime `Reconcile` runs after a Subscribe
event, it currently calls `reconcileOne(..., triggerEvent)`. To
preserve subscribe-bypass semantics (no hash short-circuit on
subscribe events), the event source needs to set a marker the
predicate / `Reconcile` can read. Cleanest approach: a per-CR
annotation set by the watcher (`config.cisco.vk/subscribe-tick`)
that increments on every event; the reconciler reads + clears it
to convert into `triggerSubscribe`.

**Acceptance.**

- Unit test: Subscribe event source delivery of a generic event for
  `(deviceName, namespace)` enqueues a Reconcile request.
- Unit test: a Reconcile triggered via the subscribe path bypasses
  the hash short-circuit even when generation/hash/InSync match.
- Existing Wave 1B unit tests stay green.
- Live retest (operator-scheduled): change device hostname out of
  band against the lab Cat9K with the per-pod controller; observe
  drift surfaced within seconds, not at the next 5-minute
  `driftDetectInterval` tick.

**Effort.** ~1 engineer-day code, ~0.5 day tests + live retest.

### Wave 5A-fu — structured gNMI paths + production registration (~3 ed)

**What.** Two distinct issues in the Wave 5A "fix":

1. **`parseGNMIPath` splits on `/` before parsing keyed values.**
   `GigabitEthernet=0/0/0` becomes `[GigabitEthernet=0, 0, 0]` —
   three path elements with the first two characters of the key
   stuffed into the wrong segment. gNMI Set/Delete against any
   list whose key value contains `/` (interfaces, prefixes,
   IPv6 addresses) emits the wrong path. This is a parser bug
   independent of the registry.
2. **Production startup never calls `schema.LoadFamilies`.** The
   only production code that touches families.yaml is
   `tools/cisco-vk-config-docs` (the docs generator). The cisco-vk
   binary uses `iosxebuilder.KeyRulesForXE()` (a hardcoded map for
   the merger) and `LoadYANGReleaseTags` (which calls
   `schema.LoadYANGReleases` only). The Wave 5A registry is
   therefore unpopulated in production; only tests that directly
   call `LoadFamilies` see the registered keys. The Wave 5A unit
   tests passed for that reason.
3. **Composite-key handling is wrong.** For a YANG list keyed by
   `[type, name]`, the current registry stores the first key
   field (`type`). But interface paths in netascode use concrete
   path segments (`GigabitEthernet`, `TwoGigabitEthernet`, …) that
   already encode the type. The gNMI list key is just `name`, not
   `type` — so registering `type` as the first field is wrong.

**Approach.**

The reviewer's recommendation is correct: stop parsing key metadata
out of ambiguous string paths. Move to a structured representation.
Three steps:

1. **Add `transport.Op.PathSpec`** — a structured path representation
   `[]PathElem{Name, Key map[string]string}`. Op continues to carry
   the legacy string `Path` for callers (cisco-vk-config-lint
   offline mode, the existing RESTCONF transport that doesn't need
   structure). gNMI transport prefers `PathSpec` when set, falling
   back to `parseGNMIPath` only when `PathSpec` is nil.
2. **Writers emit `PathSpec`** for keyed-list paths. The writers
   already know the key field name and value at op-construction
   time — there's no parsing involved on their side. Single-grep
   refactor across keyed-list writers; existing
   `transport.Op{Verb,Path,Body}` literals get a `PathSpec` peer.
3. **Production registration goes via the writer registry, not
   schema side-effect.** The `iosxebuilder` package already has a
   registration entrypoint (`KeyRulesForXE`); add a parallel
   `RegisterGNMIPathKeysForXE(ctx)` that the cisco-vk binary calls
   at startup. The schema-yaml-as-side-effect path stays for the
   docs generator but is no longer the production source of truth.

The legacy `parseGNMIPath` stays for the lint tool's offline mode
(no schema available), with the splits-on-`/` limitation
documented. Production code paths use `PathSpec` and never invoke
`parseGNMIPath` for an op.

**Acceptance.**

- Unit test: `transport.Op{PathSpec: ...}` produces a gNMI Set
  with the right keyed path for `GigabitEthernet 0/0/0`.
- Unit test: composite-key path-spec `name=eth-1` (not `type=...`)
  produces the right gNMI key.
- Unit test: legacy `transport.Op{Path: "..."}` (string-only) still
  works, falls back to `parseGNMIPath`, with the documented
  limitation around `/` in key values.
- Production startup wiring test: the cisco-vk binary's manager-mode
  init registers gNMI keys for every keyed-list writer.
- Live retest (operator-scheduled): apply an `interface_ethernet`
  intent against the lab Cat9K via gNMI/6030 (separately enabled);
  assert the device's `show running-config | section interface
  GigabitEthernet 0/0/0` reflects the intent.

**Effort.** ~2 engineer-days code (transport.Op extension + writer
mass-update + iosxebuilder registration + production wiring), ~0.5
day tests, ~0.5 day live retest.

### Wave 6B — credential Secret rotation reconciled (~1.5 ed)

**What.** Two distinct credential paths exist:

- `IOSXEConfig.spec.secretRefs[]` — Wave 2D added a Secret watch
  that maps to IOSXEConfig reconciles. ✅
- `CiscoDevice.spec.credentialSecretRef` — NOT addressed. Per-pod
  mode injects this Secret as a `valueFrom` env var on the
  Deployment. Kubernetes does NOT restart pods when a Secret-backed
  env var rotates (it's an unintended-but-load-bearing K8s API
  detail). The CiscoDevice controller doesn't watch credential
  Secrets either. Aggregator mode resolves the password at worker
  start and the worker's `specHash` records existence not value.

Result: rotating a device password leaves both topologies running
the old credential indefinitely.

**Approach.**

Two changes in CiscoDeviceReconciler and AggregatedReconciler:

1. **Per-pod path.** Watch Secrets via SetupWithManager. Map a
   Secret event to all CiscoDevices in the same namespace whose
   `spec.credentialSecretRef.name` matches. On match, annotate
   the per-device Deployment with
   `cisco.vk/credential-resourceVersion: <Secret.metadata.resourceVersion>`.
   That annotation change rolls the Deployment naturally (it's
   on the pod template, so the ReplicaSet rolls). Using the Secret's
   `resourceVersion` as the rotation key keeps the actual Secret
   data out of the Deployment (the existing `valueFrom` env stays;
   the annotation is just a rollout signal).
2. **Aggregator path.** Watch Secrets the same way; on match,
   restart the affected device worker. Update `specHash` to
   include either the Secret's resourceVersion or a digest of the
   resolved password so `specHash` change detection actually
   reflects credential rotation.

For both: scope the watch to Secrets *that are referenced by some
CiscoDevice's credentialSecretRef*. Use a fieldindexer or a
mapper-style EnqueueRequestsFromMapFunc + per-CiscoDevice match.
Indexer is cleaner; fall back to mapper if indexer adds too much
boilerplate.

Cross-namespace Secret references are not supported today (the
existing controller resolves Secrets in the same namespace as the
CiscoDevice); preserve that restriction.

**Acceptance.**

- Unit test: Secret update for a referenced
  credentialSecretRef → CiscoDevice's per-device Deployment
  template gets a fresh
  `cisco.vk/credential-resourceVersion` annotation.
- Unit test: unrelated Secret update (no CiscoDevice references it)
  → no Deployment change, no per-device noise.
- Unit test (aggregator): Secret update → worker restarts; specHash
  reflects the change.
- Live retest (operator-scheduled): rotate the lab Cat9K's password
  via secret update + observe the per-device pod restart and use
  the new credential.

**Effort.** ~1 engineer-day code (per-pod + aggregator), ~0.5 day
tests + live retest.

---

## 4. Sequencing and effort

Total estimate: **~12–17 engineer-days** for the full plan.

| Wave | Scope | Engineer-days | Gating |
|---|---|---|---|
| 0-fu | Status walk-back | ~0.25 | Required before any FU wave starts |
| 1A-fu | NETCONF candidate-Fetch + CLI in tx scope | ~3 | P1 |
| 4A-fu | configPrereqs teardown that the engine accepts | ~2 | P1 |
| 6A | Subscribe in per-pod controller-runtime | ~1.5 | P2 |
| 5A-fu | Structured gNMI paths + production registration | ~3 | P2 (largest single piece) |
| 6B | Credential Secret rotation | ~1.5 | P2 |

Recommended execution order:

1. **Status walk-back first** (~0.25d, immediate, single PR).
2. **Wave 1A-fu and Wave 4A-fu in parallel** — both are P1 and
   both block re-claiming day-2 readiness. Independent code
   surfaces.
3. **Wave 6A** — short, idiomatic, restores the advertised
   Subscribe fast path in the default topology.
4. **Wave 5A-fu** — largest. Independent surface, can run in
   parallel with anything else.
5. **Wave 6B** — last; lowest blast radius; complements Wave 2D.

---

## 5. What "shippable for day-2" means after THIS plan

The acceptance criteria from `external-review-response.md` §6
remain in force; this plan adds five more:

1. NETCONF transactional commit succeeds end-to-end against the
   lab Cat9K with at least one CLI template block in the intent.
2. configPrereqs deletion against the lab Cat9K leaves the device
   `show running-config` clean of any prereq state the controller
   created.
3. Per-pod Subscribe event triggers a Reconcile within seconds of
   an out-of-band device change (no driftDetectInterval wait).
4. gNMI Set against `interface_ethernet[GigabitEthernet=0/0/0]`
   produces the correct keyed path and lands on the device.
5. Rotating a CiscoDevice's credentialSecretRef Secret causes the
   per-device pod to restart within one Secret-update tick AND the
   aggregator worker to restart within one tick; unrelated Secret
   changes don't cause noise.

Until all five are green, `implementation-status.md` §1's day-2
claim stays walked-back.

---

## 6. What this plan does NOT address

Out of scope, intentionally:

- **CRD v1 promotion.** Tracked in [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md).
- **Log unification.** Tracked in [`log-unification-plan.md`](log-unification-plan.md).
- **Phase-8 external residuals.** Tracked in [`phase-8-residuals.md`](phase-8-residuals.md).
- **Composite-key gNMI lists with multiple non-path-encoded keys.**
  No current netascode family needs this; tracked as a follow-up
  if a future family does.
- **NETCONF confirmed-commit.** The confirmed-commit:1.0 capability
  is parsed but not yet consumed. Independent of this plan; it
  belongs in a future Phase-7-bis hardening pass.

---

## 7. Process notes

- This document is a **plan** at the point of authoring. Each Wave
  is intended to be a separate PR/commit, reviewable on its own.
- The status-doc walk-back lands as a single small commit alongside
  this RFC, before any code change starts.
- The follow-up review's two main lessons that this plan internalises:
  - **Fake-client tests are not a substitute for envtest** when CRD
    schema validation is part of the contract you're testing
    (FU-2). Wave 4A-fu adds an envtest-style or schema-validating
    test for the teardown path.
  - **A side-effect-driven registry is fragile when the side-effect
    is in a code path the production binary doesn't execute** (FU-4).
    Wave 5A-fu wires the registration through the production
    startup path explicitly.
- The reviewer's bottom line — *"the implementation is substantially
  improved, but not complete"* — is the operating principle of this
  plan. Closing FU-1..FU-5 closes it.
