# Wave 10 — confirmed-commit and atomic replace for low-blast-radius risky configurations

**Branch the plan applies to:** future PR, after `pr/johalley/ciscoconfig_xe` merges.
**Status:** plan, not implementation. Filed in response to a review question on whether the current NETCONF transactional path uses RFC 6241 §8.4 confirmed-commit; investigation found the capability is declared as a constant but never advertised or used.
**Estimated effort:** ~3 engineer-days for both features combined.
**Audience:** maintainer scoping the next behavioural addition; operator who needs to apply risky changes (BGP, ACL, management-plane modifications) with auto-revert on connectivity loss.

---

## 1. Problem statement

Today's NETCONF transactional path closes the *Mutate-error* risk: if any per-family Apply fails, the deferred `<discard-changes/>` rolls back the candidate datastore before any commit reaches running. The Wave 1A-fu candidate-aware `TxFetcher` extends this so the post-Apply verify Fetch reads from candidate rather than running, catching structural problems before commit.

But two real production-readiness gaps remain:

### 1.1 Loss-of-management — solved by confirmed-commit

After `Commit` returns success, the change is durable. There is no safety timer. If the change locks the controller out of the device — an ACL on the management interface, a routing change that breaks the management VRF, an `ip http secure-server` toggle that drops the in-flight RESTCONF session — the controller loses contact before it can revert. The candidate datastore is empty (already merged), so `Discard` no longer helps. Manual console rollback is the only path.

This is exactly the failure mode RFC 6241 §8.4 confirmed-commit was specified to prevent. The server commits *tentatively*, starts a `<confirm-timeout>` countdown, and **automatically reverts running back to its pre-commit state if the client doesn't send a follow-up `<commit/>` within the window**. A controller that has lost its session cannot send the follow-up, and the device self-heals.

### 1.2 Partial-drift — solved by atomic replace

The current engine architecture is per-family: each `SectionWriter.Diff` returns Ops, each Apply mutates a slice of the candidate, and a multi-family CR is the union of those independent mutations. Two real consequences:

- **Cross-family ordering is incidental.** If `interface_ethernet` writes succeed but `vrf` write fails, the discard rolls everything back — *but only because the engine wrapped them in one candidate*. The per-family Diff/Apply pattern doesn't enforce that ordering on the wire; it relies on the transaction boundary.
- **Removed-from-intent entries persist unless `pruneOnRelinquish=true`.** A user who removes VLAN 200 from `iosxe.devices[0].configuration.vlan.vlans[]` sees no device-side change unless the CR opts into the authoritative-prune flag. This is the additive-day-0 default, which is correct for most operators most of the time — but it leaves "atomic intent" (the entire CR's intent IS the device's state for these families) as an opt-in pattern with weak guarantees.

Atomic replace tightens that contract: the operator declares "this CR's intent IS the complete state for these managed families on this device, period." The engine emits ops to bring the device-side state into exact agreement — adding what's missing AND deleting what's extra — as one transaction.

### 1.3 Why the two features pair

Either alone is incomplete:

- **Confirmed-commit alone:** the partial-drift problem still exists. A risky change that lands cleanly per-family but produces inconsistent cross-family state can pass verify, get confirmed, and leave the device with an internally-incoherent config.
- **Atomic replace alone:** all-or-nothing on the device side, but if commit kicks the controller out, no auto-recovery. The atomic batch sticks; the controller is locked out.
- **Both enabled together:** the change is treated as a single all-or-nothing unit, AND if it breaks management, the device reverts itself.

This is the "*derisk the configuration actions, allowing for more risky configurations to be applied*" primitive the review question is asking about.

---

## 2. Design

### 2.1 Per-CR knobs

Add two fields to `IOSXEConfigTemplateSpec`:

```go
// ConfirmTimeoutSeconds enables RFC 6241 §8.4 confirmed-commit. When
// non-zero, the engine commits the candidate datastore tentatively
// and runs the verify phase against running. If verify succeeds the
// commit is confirmed; otherwise the device auto-reverts at the
// timeout. Requires spec.transactional=true and a transport that
// advertises :confirmed-commit:1.0 (or :1.1). Defaults to zero
// (unconfirmed) to preserve existing behaviour. A zero value when
// transactional=true falls back to plain <commit/>; the operator
// must set this explicitly to opt in to the auto-revert safety net.
//
// Recommended values: 30 for ACL / management-plane changes; 60-120
// for BGP / routing-protocol changes that need adjacency
// re-establishment time before the controller can verify reachability.
//
// +kubebuilder:default=0
// +kubebuilder:validation:Minimum=0
// +kubebuilder:validation:Maximum=300
// +optional
ConfirmTimeoutSeconds int32 `json:"confirmTimeoutSeconds,omitempty"`

// AtomicReplace opts into all-or-nothing replacement semantics for
// the CR's managed families. When true, the engine treats the
// resolved intent as the COMPLETE device-side state for those
// families: device-side entries not in the intent are deleted in
// the same transaction that adds the new entries. Requires
// spec.transactional=true. Mutually compatible with — and stronger
// than — pruneOnRelinquish: pruneOnRelinquish=true does
// per-family authoritative pruning continuously, while
// atomicReplace=true also enforces cross-family ordering inside
// the transaction (e.g. a VRF removal that is referenced by an
// interface that is also being removed will be ordered correctly).
//
// Defaults to false to preserve the existing additive day-0
// behaviour. Operators flip to true on CRs whose intent is the
// authoritative source for those families' device-side state —
// typically a single per-device CR that owns interface_ethernet,
// vlan, vrf, and the routing protocols.
//
// +kubebuilder:default=false
// +optional
AtomicReplace bool `json:"atomicReplace,omitempty"`
```

Both default to off. Both are opt-in. Both require `transactional: true`.

### 2.2 Transport contract

Extend the optional-interface pattern (mirror `TxFetcher` and `SubscribeCapable`):

```go
// ConfirmedCommitter is implemented by transports that support
// RFC 6241 §8.4 confirmed-commit. The engine type-asserts at
// transactional setup time and falls back to plain Commit when
// the assertion fails or when Capabilities.SupportsConfirmedCommit
// is false. Implementations MUST advertise the capability in their
// hello phase (NETCONF) or report it via Capabilities (gNMI/
// RESTCONF transports cannot satisfy this interface today since
// neither protocol has a comparable primitive).
type ConfirmedCommitter interface {
    // CommitConfirmed issues a tentative commit. The candidate
    // datastore is merged into running, but the server starts a
    // timer; if ConfirmCommit is not called within timeout the
    // server reverts running to its pre-commit state.
    CommitConfirmed(ctx context.Context, tx TxHandle, timeout time.Duration) error

    // ConfirmCommit cancels the auto-revert timer and makes the
    // tentative commit permanent. Idempotent.
    ConfirmCommit(ctx context.Context) error
}
```

The existing `Commit(ctx, tx) error` keeps semantics unchanged — non-confirmed callers see no behavioural change.

Add `SupportsConfirmedCommit bool` to `Capabilities`. NETCONF probes for `urn:ietf:params:netconf:capability:confirmed-commit:1.0` (or `:1.1`) at hello time and sets the field accordingly. RESTCONF and gNMI transports report `false` and the engine falls back to plain Commit.

For atomic replace, no transport-contract change is needed — the existing `transport.Op{Verb: VerbReplace}` is the wire primitive. The engine composes the per-family Replace ops into a single transaction with explicit before-after dependency ordering.

### 2.3 Engine-level state machine changes

#### 2.3.1 Confirmed-commit path

When `ConfirmTimeoutSeconds > 0` AND `Capabilities.SupportsConfirmedCommit` AND `transport implements ConfirmedCommitter`:

```text
StartTransaction
for each family: Fetch (candidate) → Diff → Apply → candidate-Verify
if any failure → Discard → Phase=Failed   [unchanged]

CommitConfirmed(timeout)                  [NEW — tentative commit]
running-Verify                            [NEW — Fetch from running, re-Diff]
  ↓                                        each family must see no drift
if running-Verify fails → DO NOT call ConfirmCommit;
                          let timeout fire; device auto-reverts.
                          Phase=Failed with explicit "reverted by confirm-timeout"
                          message and a counter increment.
if running-Verify succeeds → ConfirmCommit() → Phase=InSync   [unchanged terminal]
```

Two new metric counter outcomes:

```text
cisco_vk_config_transactions_total{outcome="confirmed"}     # tentative→confirmed path
cisco_vk_config_transactions_total{outcome="auto_reverted"} # tentative→timeout path
```

Both are useful to alert on: `confirmed` rate at steady state should equal the `commit` rate from the legacy path; a non-zero `auto_reverted` rate is the operator's signal that risky changes are reverting and the design is working.

#### 2.3.2 Atomic-replace composition

When `AtomicReplace=true` AND `transactional=true`:

The engine no longer drives per-family Apply in a flat loop. It composes a **delete-set** (device-side entries not in resolved intent) and an **add-set** (resolved-intent entries not on device) per family, then orders them across families using the family dependency declarations in [`schema/families.yaml`](../../internal/drivers/iosxe/configdriver/schema/families.yaml) (the existing `depends_on` field).

Wire-level: the engine emits one `<edit-config>` RPC per family with `<default-operation>replace</default-operation>` for delete-set + add-set merged. The candidate ends up containing exactly the resolved-intent state for those families, regardless of what was there before.

Cross-family ordering example:
- Removing a VRF that is bound to an interface: interface_ethernet's delete-set runs *before* vrf's delete-set, because `interface_ethernet.depends_on: [vrf]`.
- Removing an ACL bound to a route-map: route_map's delete-set runs before access_list_extended's delete-set.

The `families.yaml` `depends_on` field already exists for this purpose; today the engine doesn't use it for ordering because the per-family pattern doesn't need to. Atomic replace is the use case that activates it.

#### 2.3.3 Combined: confirmed-commit + atomic-replace

When both are on, the flow is:

```text
StartTransaction (lock candidate)
for each family in dependency order:
  Fetch (candidate)
  Diff (compute add-set + delete-set against intent)
  Apply (emit ops with <default-operation>replace</default-operation>)
  candidate-Verify (atomic: assert candidate exactly matches intent)
if any failure → Discard → Phase=Failed

CommitConfirmed(timeout)
running-Verify (re-Fetch all managed families from running, assert equal to intent)
if any failure → let timeout fire → Phase=Failed{auto_reverted}
ConfirmCommit() → Phase=InSync
```

The post-commit `running-Verify` is the new safety bar. It is what the timeout protects: if `running-Verify` itself takes longer than the controller can keep its session for (e.g. because the change broke the management plane), the controller never reaches `ConfirmCommit` and the device reverts.

### 2.4 Backwards compatibility

- `ConfirmTimeoutSeconds` defaults to 0. Existing CRs see no behaviour change.
- `AtomicReplace` defaults to false. Existing CRs see no behaviour change.
- Plain `Commit(ctx, tx) error` semantics are preserved for callers that don't opt in.
- The `:confirmed-commit:1.0` capability is added to `clientCapabilities` regardless — that's a one-way capability advertisement and changes nothing for non-opting CRs.
- Transports that don't implement `ConfirmedCommitter` (RESTCONF, gNMI) silently fall back to plain Commit even when the CR opts in. The engine emits a one-time Warning event on the CR explaining the fallback and clears `confirmTimeoutSeconds` from the effective behavior. This is not a hard error because operators may be running the same CR shape across heterogeneous transports.

---

## 3. Test additions

### 3.1 Unit tests

In [`internal/drivers/iosxe/configdriver/engine/`](../../internal/drivers/iosxe/configdriver/engine/):

- `TestConfirmedCommitHappyPath` — extend `txTransport` to track `commitConfirmedCalls` and `confirmCommitCalls`. Drive `Engine.Reconcile` with a CR carrying `ConfirmTimeoutSeconds=30` and `transactional=true`. Assert the call sequence is `StartTransaction → Mutate → Fetch (candidate) → CommitConfirmed → Fetch (running) → ConfirmCommit`. Assert the metric `cisco_vk_config_transactions_total{outcome="confirmed"}` increments by 1.
- `TestConfirmedCommitAutoRevertOnVerifyFailure` — same fixture but the running-Verify sees drift (writer's idempotency check finds residual). Assert `ConfirmCommit` was NOT called; assert `Phase=Failed`; assert metric `outcome="auto_reverted"` increments.
- `TestConfirmedCommitFallbackWhenUnsupported` — CR opts in, transport reports `SupportsConfirmedCommit=false`. Assert engine emits Warning event, falls back to plain Commit, and the CR reaches `Phase=InSync` (not `Failed`).
- `TestAtomicReplaceComputesAddAndDeleteSets` — fixture with a writer whose Diff returns one add-op AND one delete-op for residual device-side state. Assert the engine emits both with `<default-operation>replace</default-operation>` semantics in the same transaction.
- `TestAtomicReplaceRespectsFamilyDependencyOrder` — multi-family fixture with `interface_ethernet` and `vrf`; assert interface delete-ops emitted before VRF delete-ops, and VRF add-ops before interface add-ops, per the schema `depends_on` declaration.
- `TestAtomicReplaceCombinedWithConfirmedCommit` — full happy path with both enabled. Assert the candidate is the *intent* (not a merge), running-Verify passes, ConfirmCommit fires.

### 3.2 envtest

In [`internal/provider/envtest_apiserver_smoke_test.go`](../../internal/provider/envtest_apiserver_smoke_test.go):

- `TestEnvtest_ConfirmTimeoutSecondsAdmittedByApiserver` — write `spec.confirmTimeoutSeconds: 60` through the real apiserver, assert it round-trips. Negative control: set to `-1` and assert the apiserver rejects (kubebuilder Minimum=0).
- `TestEnvtest_AtomicReplaceFieldAdmitted` — same shape, asserts the boolean field round-trips.
- `TestEnvtest_ConfirmTimeoutSecondsMaximumEnforced` — write 301, assert rejection (Maximum=300).

### 3.3 New release-blocker test 08

`docs/rfcs/final/release-blocker-tests/08-confirmed-commit-auto-revert/`:

The headline live test that proves auto-revert works end-to-end. The test deliberately submits a config change that breaks the controller's session (e.g. an ACL on the management interface that drops the controller's source IP). Confirmed-commit fires, the controller cannot reach the device to confirm, and after `ConfirmTimeoutSeconds` the device auto-reverts. The test verifies:

- Device-side state during the timeout window includes the broken config (via console-side observation, scripted via the operator's existing OOB tooling — outside the controller's direct path).
- Device-side state after the timeout window matches the pre-test state (auto-revert worked).
- `cisco_vk_config_transactions_total{outcome="auto_reverted"}` increments by 1.
- The CR's `status.phase=Failed` with a Reason like `AutoRevertedByConfirmTimeout`.

This is the single most operationally-meaningful test of the whole feature. Without it, the auto-revert path is theoretical. Operator-scheduled, requires a confirmed willingness to deliberately break the management session for the timeout window.

A complementary `docs/rfcs/final/release-blocker-tests/09-atomic-replace-cross-family/` covers the partial-drift scenario: a CR initially declares `vlan: [10, 20, 30]` and `vrf: [MGMT]`; second apply removes VLAN 20 and the VRF; verify both removals happen as one transaction (no intermediate state where the VRF is gone but VLAN 20 is still there).

---

## 4. Implementation slicing

Recommended Wave-10 PR breakdown, mirroring the per-wave pattern the rest of the chain uses:

| Sub-wave | Scope | Effort |
|---|---|---|
| 10.1 | NETCONF capability advertisement; `Capabilities.SupportsConfirmedCommit`; transport-level `ConfirmedCommitter` impl with unit tests against the existing NETCONF mock session | ~0.5 ed |
| 10.2 | Engine-side confirmed-commit path; new metric outcomes; `spec.confirmTimeoutSeconds` API field; CRD regen; envtest admission test | ~1 ed |
| 10.3 | Engine-side atomic-replace composition (add-set + delete-set computation, family dependency ordering); `spec.atomicReplace` API field; CRD regen | ~1 ed |
| 10.4 | Combined-mode unit tests; release-blocker tests 08 + 09; runbook updates; architectural-review-final §6.B closure | ~0.5 ed |

Total ~3 engineer-days.

---

## 5. Sequencing relative to other deferred work

Order of dedicated post-merge PRs, in priority order:

1. **Wave 10 (this RFC)** — confirmed-commit + atomic replace. Risk-reduction primitive. Should land before any operator considers `driftPolicy: revert` on a production CR for a high-blast-radius family (BGP, ACL, management plane).
2. **Phase-10 cosmetic relocation** ([`driver-extension-guide.md`](driver-extension-guide.md) §7) — `internal/drivers/iosxe/configdriver/...` → `internal/configdriver/...`. Mechanical; does not depend on Wave 10.
3. **Log unification** ([`log-unification-plan.md`](log-unification-plan.md)) — slog backend. Independent of Wave 10.
4. **CRD v1 promotion** ([`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)) — should include `confirmTimeoutSeconds` and `atomicReplace` in the v1 cut, so they ship with stable-API guarantees. Wave 10 should land in v1alpha1 first; v1 promotion is the breaking-change opportunity to refine the field shapes if needed.

The conversion-webhook PR (envtest infrastructure) lands with item 4. Wave 10's envtest additions therefore live alongside the existing focused envtest in [`internal/provider/envtest_apiserver_smoke_test.go`](../../internal/provider/envtest_apiserver_smoke_test.go) — same minimal-surface pattern.

---

## 6. What this plan does NOT address

Out of scope for Wave 10:

- **Confirmed-commit on RESTCONF or gNMI.** Neither protocol has a §8.4-equivalent primitive. RESTCONF has nothing; gNMI's `SetRequest` is atomic per-request but has no "tentative-then-confirm" window. CRs that opt in on RESTCONF/gNMI transports get the warned-and-fall-back behaviour described in §2.4.
- **Multi-CR atomic replace.** Atomic replace is per-CR. Two CRs targeting the same device with overlapping `managedFamilies` cannot be jointly atomic — the lease layer arbitrates and one wins. Cross-CR coordination is out of scope and would need a different primitive (a "transaction CR" that fans out to children).
- **Multi-device atomic replace.** Same point — Wave 10 is per-device. Multi-device atomic apply (a `IOSXEConfigBundle` whose all-or-nothing semantics span every member device) needs the bundle controller to manage a distributed transaction, which is a separate, much larger plan.
- **`<persist>` parameter** for confirmed-commit:1.1 (RFC 6241 §8.4.5). The persist-id mechanism lets a confirmed commit survive session drops by being re-confirmed from a different session. Useful but not in scope; today's design assumes the same controller process that issued `CommitConfirmed` issues `ConfirmCommit`. A controller restart mid-confirm-window would let the device auto-revert, which is the safe failure mode.
- **Configuration validate-only mode** (NETCONF `<validate><source><candidate/></source></validate>`). A pre-commit syntactic check against the candidate. Useful but fully orthogonal to confirmed-commit; could be a small follow-up in Wave 11.

---

## 7. Cross-references

| Topic | RFC |
|---|---|
| RFC 6241 §8.4 (confirmed-commit:1.0/1.1) | [IETF RFC 6241](https://www.rfc-editor.org/rfc/rfc6241#section-8.4) |
| Existing transactional path | [`internal/drivers/iosxe/configdriver/engine/engine.go`](../../internal/drivers/iosxe/configdriver/engine/engine.go) lines 250-358; closing wave 1A-fu in [`external-review-followup-response.md`](external-review-followup-response.md) |
| Family dependency declarations | [`internal/drivers/iosxe/configdriver/schema/families.yaml`](../../internal/drivers/iosxe/configdriver/schema/families.yaml) |
| `pruneOnRelinquish` semantics (related but weaker than atomic replace) | [`api/config/v1alpha1/iosxeconfig_types.go`](../../api/config/v1alpha1/iosxeconfig_types.go); closing waves 4A-fu + 7A.4 |
| Architectural overview deferred-items register | [`final/architectural-review-final.md`](final/architectural-review-final.md) §6.B (this plan adds row 4) |
| Test-discipline lessons that inform §3 above | [`implementation-status.md`](implementation-status.md) §1.2 |
| Release-blocker test pattern that 08 + 09 will follow | [`final/release-blocker-tests/RUNBOOK.md`](final/release-blocker-tests/RUNBOOK.md) |
| v1 promotion plan (Wave 10 fields should propagate to v1) | [`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md) |
