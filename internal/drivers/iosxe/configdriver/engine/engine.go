// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// ErrTransactionalCLIUnsupported is returned when the resolved
// intent combines spec.transactional=true with one or more CLI
// template blocks. NETCONF's Cisco-IA cli-config-data RPC operates
// on running config directly — there is no candidate-bound CLI
// path today, so the engine cannot guarantee atomicity across the
// structured-ops + CLI mix. Reject before any mutation; operator
// drops spec.transactional OR removes the CLI templates from the
// resolved intent and re-applies.
//
// Wave 7A.1 (external-review-next-actions Finding #1).
var ErrTransactionalCLIUnsupported = errors.New(
	"transactional NETCONF apply does not support CLI template blocks: " +
		"Cisco-IA cli-config-data writes directly to running and cannot be " +
		"rolled back via candidate-datastore Discard")

// Phase labels the reconciler writes to status.phase. The set is closed;
// any other string in status.phase is a bug.
const (
	PhasePending    = "Pending"
	PhaseValidating = "Validating"
	PhasePlanning   = "Planning"
	PhaseApplying   = "Applying"
	PhaseVerifying  = "Verifying"
	PhaseInSync     = "InSync"
	PhaseDrifted    = "Drifted"
	PhaseFailed     = "Failed"
	PhasePaused     = "Paused"
	// PhaseLeaseBlocked is the transient state when one or more
	// requested families is currently leased by another reconciler
	// (a different IOSXEConfig CR claiming the same family, or the
	// other side of a Wave 7A.3 runtime-suffixed-identity rollout
	// overlap). The engine did NOT touch the device on this tick;
	// the next tick should requeue at sub-TTL cadence so the
	// blocking holder has a chance to release/expire.
	//
	// Wave 8.2 (external-review-wave7-residuals Finding #2). The
	// previous behaviour routed lease conflicts through the engine
	// as PhaseFailed (all-blocked: empty ManagedFamilies trips
	// validate()) or PhaseInSync (partial-block: the engine reports
	// the subset it owned as in-sync, masking the missed families).
	// Both lied to operators about what happened on the device.
	PhaseLeaseBlocked = "LeaseBlocked"
)

// Engine reconciles one ResolvedIntent against one device. It owns the
// state-machine transitions and the per-family iteration; it does NOT
// own the informer, queue, or status writes — those belong to the
// outer reconciler in internal/provider, which calls Engine.Reconcile
// once per tick.
type Engine struct {
	// Transport is the device channel. Lifetime is caller-owned.
	Transport transport.Interface

	// Lookup returns the writer registered for a family, or nil if
	// none is registered. Injected (rather than calling writers.Get
	// directly) so tests can supply a fake writer set.
	Lookup func(family string) writers.SectionWriter

	// applyTransport is the transport handed to writers during the
	// apply loop. When the tick is non-transactional it equals
	// Transport. When the tick opens a transaction (NETCONF + spec
	// asks for it), apply-loop callsites swap in a transactionalView
	// so writers' Mutate calls land in the candidate datastore
	// instead of the running config. Reset before/after each tick;
	// not part of the public API.
	applyTransport transport.Interface
}

// Result is the full summary of a reconcile tick. It is intentionally
// verbose: every field the outer reconciler needs for status reporting
// is surfaced here rather than being re-discovered by re-calling the
// engine.
type Result struct {
	// Phase is the terminal phase of this tick. One of PhaseInSync,
	// PhaseDrifted, PhaseFailed, or PhasePaused. The transitional
	// phases (Validating, Planning, Applying, Verifying) are observable
	// by subscribing to Events; they never appear as a final Phase.
	Phase string

	// FamilyStatuses is one entry per family in ManagedFamilies,
	// describing its post-tick state. Families ordered as in the
	// intent's ManagedFamilies so the result is stable.
	FamilyStatuses []FamilyStatus

	// Drift is populated when at least one family is Drifted under a
	// report policy, or when a revert policy has a post-apply gap.
	Drift []DriftEntry

	// Err is non-nil when the tick failed before every family had its
	// chance to run. Per-family errors are also surfaced on the
	// FamilyStatuses entry; Err is the engine-level top-line failure.
	Err error

	// YangVersion is the YANG release the resolver pinned for this
	// reconcile (ResolvedIntent.TargetYangVersion). Surfaced on the
	// CR's status.sourceYangVersion after a successful apply so the
	// audit log and the per-CR status agree on what release drove
	// the device write.
	YangVersion string

	// SaveStartupOK is true when spec.writeStartup was requested,
	// the transport supports it, AND the post-apply SaveStartup call
	// succeeded. False otherwise (including the no-op case).
	SaveStartupOK bool

	// SaveStartupErr is non-nil when SaveStartup was attempted and
	// failed. The apply itself is already committed by then; the
	// outer reconciler treats this as a non-fatal warning.
	SaveStartupErr error

	// DeviceTouched is true when the engine's reconcileFamily loop
	// actually called Fetch / Diff / Apply on the device for at
	// least one family. Reconciler.recordResult uses this to decide
	// whether to bump status.lastDeviceCheck — Wave 1B's
	// "device freshness" timestamp.
	//
	// Wave 8.2 (external-review-wave7-residuals Finding #2). Pre-fix
	// recordResult unconditionally bumped LastDeviceCheck on every
	// reconcile call, even when every family was lease-blocked and
	// no device-side work happened. That stale freshness timestamp
	// then short-circuited subsequent ticks via Wave 1B's
	// dueForDriftCheck logic, hiding the missed reconciles.
	DeviceTouched bool

	// ConfirmedCommitFallback carries a human-readable reason when
	// the CR requested confirmed-commit (spec.confirmTimeoutSeconds >
	// 0) but the engine could not use it and fell back to plain
	// Commit. Empty in three cases: (a) confirmed-commit was
	// requested AND used; (b) confirmed-commit was not requested;
	// (c) no transactional path was taken at all.
	//
	// The reconciler emits a one-time Warning event on the CR
	// surfacing this reason so operators know why their auto-revert
	// safety net didn't engage. Common values:
	//
	//   "transport does not implement ConfirmedCommitter"
	//     RESTCONF or gNMI transport in use; no protocol equivalent
	//     (RESTCONF) or Cisco devices haven't shipped the gNMI
	//     extension yet (gNMI).
	//   "device did not advertise confirmed-commit:1.0"
	//     NETCONF transport but an older IOS-XE image without the
	//     RFC 6241 §8.4 capability.
	//   "non-transactional reconcile"
	//     spec.transactional=false; confirmed-commit needs a
	//     candidate datastore.
	//
	// Wave 10.
	ConfirmedCommitFallback string

	// ConfirmedCommitUsed is true iff the engine took the auto-revert
	// path: CommitConfirmed succeeded, running-Verify passed, and
	// ConfirmCommit fired. False on every other path including the
	// successful plain-Commit path. Useful for the reconciler's
	// status-condition emission and for the architecture's "did we
	// actually exercise the safety net" assertion in live tests.
	// Wave 10.
	ConfirmedCommitUsed bool
}

// FamilyStatus is the per-family outcome of a tick.
type FamilyStatus struct {
	Name    string
	State   string // InSync | Drifted | ApplyError | Skipped | Unsupported
	Entries int32
	Message string
	OpCount int
}

// DriftEntry matches the configv1alpha1.DriftEntry shape without
// pulling the API package into the engine's hot path.
type DriftEntry struct {
	Family   string
	Path     string
	Desired  string
	Observed string
}

// Reconcile executes one tick of the state machine against intent.
// The flow is:
//
//	Validating → Planning → Applying → Verifying → InSync / Drifted / Failed
//
// Per-family execution is linear (no parallel writes); the transport's
// SessionLock handles coexistence with apphosting, but two families in
// flight at once would complicate rollback semantics with no real gain
// on a single device.
func (e *Engine) Reconcile(ctx context.Context, res *intent.ResolvedIntent) Result {
	start := time.Now()
	if res == nil {
		r := Result{Phase: PhaseFailed, Err: errors.New("engine.Reconcile: nil intent")}
		recordResult("", r, time.Since(start).Seconds())
		return r
	}

	if res.DriftPolicy == configv1alpha1.DriftPolicyPause {
		r := Result{Phase: PhasePaused}
		recordResult(res.DeviceName, r, time.Since(start).Seconds())
		return r
	}

	if err := e.validate(res); err != nil {
		r := Result{Phase: PhaseFailed, Err: fmt.Errorf("Validating: %w", err)}
		recordResult(res.DeviceName, r, time.Since(start).Seconds())
		return r
	}

	result := Result{
		FamilyStatuses: make([]FamilyStatus, 0, len(res.ManagedFamilies)),
		YangVersion:    res.TargetYangVersion,
	}
	anyDrift := false
	anyFailure := false

	// Transaction orchestration. When the CR asks for transactional
	// apply AND the transport advertises support, open one
	// transaction per tick and bind it to a transactionalView that
	// writers see in place of the raw transport. The handle scope is
	// the entire family loop and the post-loop CLI block; commit
	// happens on full success, discard on any failure or panic.
	//
	// External-review Finding #1: prior to this wiring, spec.transactional
	// was inert because the engine handed writers e.Transport directly
	// regardless. NETCONF therefore wrote running on every Mutate call.
	caps := e.Transport.Capabilities()
	transactional := res.Transactional && caps.SupportsTransactions

	// Wave 7A.1 (external-review-next-actions Finding #1): reject
	// transactional + CLI template combination at the engine level
	// before any mutation runs. The NETCONF transport's pushCLI uses
	// Cisco-IA cli-config-data, an RPC that operates directly on
	// running config by design — there is no candidate-bound variant
	// today. Allowing the combination would let CLI ops land in
	// running while structured ops sit in candidate; a later
	// Discard would NOT roll back the CLI changes. Failing closed
	// at the engine boundary is the safe contract.
	//
	// Operators see a clear Phase=Failed with a stable Reason
	// (TransactionalCLIUnsupported) on the Ready condition. The
	// CR's spec must drop spec.transactional or remove the CLI
	// templates from the resolved intent before the next reconcile
	// can run.
	if transactional && len(res.CLIBlocks) > 0 {
		r := Result{
			Phase: PhaseFailed,
			Err:   ErrTransactionalCLIUnsupported,
		}
		recordResult(res.DeviceName, r, time.Since(start).Seconds())
		return r
	}

	var (
		txHandle    transport.TxHandle
		txCommitted bool
	)
	if transactional {
		h, err := e.Transport.StartTransaction(ctx)
		if err != nil {
			recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "start_failed")
			r := Result{Phase: PhaseFailed, Err: fmt.Errorf("StartTransaction: %w", err)}
			recordResult(res.DeviceName, r, time.Since(start).Seconds())
			return r
		}
		txHandle = h
		e.applyTransport = newTransactionalView(e.Transport, txHandle)
		defer func() {
			// Rollback on any path that didn't reach Commit. Commit
			// flips txCommitted true; absent that flag, Discard.
			// Discard errors are logged via the result-level Err
			// when applicable but otherwise ignored — at this point
			// the apply already failed, the transport's own state
			// recovery is the operator's lever.
			if !txCommitted {
				_ = e.Transport.Discard(ctx, txHandle)
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "discard")
			}
			e.applyTransport = nil
		}()
	} else {
		e.applyTransport = e.Transport
		defer func() { e.applyTransport = nil }()
	}

	// Wave 8.2: any iteration of the family loop means the engine
	// at least attempted Fetch on the device. Set DeviceTouched
	// here (before the per-family work) so even a Fetch error
	// counts as "we contacted the device" — that's the contract
	// LastDeviceCheck records.
	if len(res.ManagedFamilies) > 0 {
		result.DeviceTouched = true
	}
	for _, family := range res.ManagedFamilies {
		fs := e.reconcileFamily(ctx, family, res)
		result.FamilyStatuses = append(result.FamilyStatuses, fs)
		switch fs.State {
		case "Drifted":
			anyDrift = true
			result.Drift = append(result.Drift, DriftEntry{
				Family: family, Path: "(family-level)", Desired: "intent", Observed: "device",
			})
		case "ApplyError", "Unsupported":
			anyFailure = true
		}
	}

	// Apply CLI-template blocks after every family has converged.
	// CLI runs last because netascode CLI templates typically
	// reference structured entities the family writers just
	// created (e.g. "interface VirtualPortGroup0 / ip address
	// ...") — pushing them before the families land would race.
	//
	// In driftPolicy=report mode CLI blocks are surfaced as drift
	// entries but not applied, matching the read-only semantics
	// the whole policy enforces.
	if !anyFailure && res.DriftPolicy != configv1alpha1.DriftPolicyReport {
		for _, block := range res.CLIBlocks {
			fs := e.applyCLIBlock(ctx, block, res)
			result.FamilyStatuses = append(result.FamilyStatuses, fs)
			if fs.State == "ApplyError" {
				anyFailure = true
			}
		}
	} else if len(res.CLIBlocks) > 0 && res.DriftPolicy == configv1alpha1.DriftPolicyReport {
		// Report mode: CLI blocks surface as drift, not apply.
		for _, block := range res.CLIBlocks {
			anyDrift = true
			result.FamilyStatuses = append(result.FamilyStatuses, FamilyStatus{
				Name:    "cli:" + block.TemplateName,
				State:   "Drifted",
				OpCount: countCLILines(block.CLI),
				Message: "CLI block withheld under driftPolicy=report",
			})
			result.Drift = append(result.Drift, DriftEntry{
				Family:  "cli:" + block.TemplateName,
				Path:    "(cli block)",
				Desired: "cli template output",
			})
		}
	}

	switch {
	case anyFailure:
		result.Phase = PhaseFailed
		result.Err = errors.New("one or more families failed to reconcile")
	case anyDrift && res.DriftPolicy == configv1alpha1.DriftPolicyReport:
		result.Phase = PhaseDrifted
	case anyDrift:
		// With a revert policy, drift that remains after apply indicates
		// something the writer could not correct. Report it as Failed
		// so the outer reconciler escalates.
		result.Phase = PhaseFailed
		result.Err = errors.New("drift persisted after revert")
	default:
		result.Phase = PhaseInSync
	}

	// Commit only if the apply path reached the end without failure.
	// Any earlier error or drift-after-revert path falls through to the
	// deferred Discard in the transaction-open block above.
	//
	// Wave 10 — when the CR opts in to confirmed-commit AND the
	// transport supports it AND the device advertised the capability,
	// take the auto-revert path: CommitConfirmed → running-Verify →
	// ConfirmCommit. Any failure on the running-Verify path leaves
	// the device's auto-revert timer running; the deferred Discard
	// releases the candidate lock without a confirmed commit, the
	// device reverts running to its pre-commit state at the timeout,
	// and we surface Phase=Failed with a clear "auto-reverted" Err.
	//
	// Backward-compat guarantees the caller depends on:
	//   - confirmTimeoutSeconds == 0 → plain Commit (existing path).
	//   - confirmTimeoutSeconds > 0 + transport doesn't implement
	//     ConfirmedCommitter → plain Commit + Result.ConfirmedCommitFallback
	//     surfaces the reason for the reconciler to event-warn the operator.
	//   - confirmTimeoutSeconds > 0 + capability not advertised →
	//     plain Commit + Result.ConfirmedCommitFallback as above.
	//   - non-transactional + confirmTimeoutSeconds > 0 → no commit at
	//     all (writes already happened via Mutate); ConfirmedCommitFallback
	//     surfaces the "non-transactional reconcile" reason.
	if transactional && result.Phase == PhaseInSync {
		useConfirmed, cc, fallbackReason := e.confirmedCommitDecision(res, caps)
		if useConfirmed {
			timeout := time.Duration(res.ConfirmTimeoutSeconds) * time.Second
			if err := cc.CommitConfirmed(ctx, txHandle, timeout); err != nil {
				result.Phase = PhaseFailed
				result.Err = fmt.Errorf("CommitConfirmed: %w", err)
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "commit_failed")
			} else if !e.runningVerify(ctx, res) {
				// running-Verify failed: do NOT call ConfirmCommit.
				// Deferred Discard cleans up the candidate lock; the
				// device's confirm-timeout timer fires and reverts
				// running to pre-commit. txCommitted stays false so
				// the deferred Discard runs.
				result.Phase = PhaseFailed
				result.Err = errors.New("running-verify failed after CommitConfirmed; device will auto-revert at confirm-timeout")
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "auto_reverted")
			} else if err := cc.ConfirmCommit(ctx); err != nil {
				// CommitConfirmed succeeded but the follow-up
				// confirm RPC failed. The device will auto-revert
				// at the timeout — this is the safe failure mode.
				result.Phase = PhaseFailed
				result.Err = fmt.Errorf("ConfirmCommit: %w", err)
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "auto_reverted")
			} else {
				txCommitted = true
				result.ConfirmedCommitUsed = true
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "confirmed")
			}
		} else {
			result.ConfirmedCommitFallback = fallbackReason
			if err := e.Transport.Commit(ctx, txHandle); err != nil {
				result.Phase = PhaseFailed
				result.Err = fmt.Errorf("Commit: %w", err)
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "commit_failed")
			} else {
				txCommitted = true
				recordTransaction(res.DeviceName, transportKindLabel(e.Transport), "commit")
			}
		}
	} else if !transactional && res.ConfirmTimeoutSeconds > 0 {
		// Non-transactional CR opted in to confirmed-commit. The
		// transport already wrote the changes via per-family Mutate;
		// there's no commit to confirm. Surface the fallback
		// without altering Phase — the reconcile may still be
		// successful at the family level.
		result.ConfirmedCommitFallback = "non-transactional reconcile"
	}

	// SaveStartup: only after a fully-clean apply (no failures, fully
	// committed, every family InSync). Failure to save startup-config
	// is non-fatal — running-config has already been written and
	// persisted; the operator gets a Warning event and an explicit
	// SaveStartupFailed surface, but the apply itself remains green.
	// External-review Finding #1: previously inert.
	if result.Phase == PhaseInSync && res.WriteStartup && caps.SupportsSaveStartup {
		if err := e.Transport.SaveStartup(ctx); err != nil {
			result.SaveStartupErr = err
			recordSaveStartup(res.DeviceName, transportKindLabel(e.Transport), "failed")
		} else {
			result.SaveStartupOK = true
			recordSaveStartup(res.DeviceName, transportKindLabel(e.Transport), "ok")
		}
	}

	recordResult(res.DeviceName, result, time.Since(start).Seconds())
	return result
}

// confirmedCommitDecision tells the Reconcile path whether to take
// the auto-revert flow or fall back to plain Commit. Returns
// (useConfirmed, ConfirmedCommitter, fallback-reason). When
// useConfirmed is true, the second return is the asserted interface
// and fallback-reason is empty. When useConfirmed is false because
// of a missing prerequisite, the third return carries a
// human-readable reason the reconciler surfaces to operators via
// Result.ConfirmedCommitFallback.
//
// Three checks, in increasing specificity:
//
//  1. CR opted in (ConfirmTimeoutSeconds > 0)? If not, return
//     useConfirmed=false with empty reason — no fallback to surface,
//     plain Commit is the operator's chosen path.
//  2. Transport implements ConfirmedCommitter? RESTCONF doesn't
//     (no protocol equivalent). gNMI doesn't yet on Cisco devices
//     (the open-standard extension exists but ships unimplemented).
//     Future-proof: when gNMI gains it, the gNMI transport just
//     needs to satisfy the interface and capability flag; no
//     change here.
//  3. Device advertised the capability in NETCONF hello? Older
//     IOS-XE images may not.
func (e *Engine) confirmedCommitDecision(res *intent.ResolvedIntent, caps transport.Capabilities) (bool, transport.ConfirmedCommitter, string) {
	if res.ConfirmTimeoutSeconds <= 0 {
		return false, nil, ""
	}
	cc, ok := e.Transport.(transport.ConfirmedCommitter)
	if !ok {
		return false, nil, "transport does not implement ConfirmedCommitter"
	}
	if !caps.SupportsConfirmedCommit {
		return false, nil, "device did not advertise confirmed-commit:1.0"
	}
	return true, cc, ""
}

// runningVerify is the post-CommitConfirmed assertion that the
// change actually took on running and the controller can still
// reach the device. Walks the resolved intent's managed families,
// re-Fetches each from the *raw* transport (NOT the
// transactionalView wrapper — at this point the candidate has
// merged into running, and we want to read running directly to
// detect operational regressions that the candidate-side verify
// could not see).
//
// Returns true iff every family's writer reports zero drift
// against the desired intent. Any error during Fetch (e.g. the
// device dropped the controller's session because the change broke
// management connectivity) returns false; the engine then declines
// to call ConfirmCommit and the device's auto-revert timer fires.
//
// Wave 10. Skips families whose writer is unregistered (no-op
// gracefully, mirroring reconcileFamily's writer-not-found path).
func (e *Engine) runningVerify(ctx context.Context, res *intent.ResolvedIntent) bool {
	for _, family := range res.ManagedFamilies {
		if e.Lookup == nil {
			return false
		}
		w := e.Lookup(family)
		if w == nil {
			// Writer not shipped for this family — skip,
			// mirroring reconcileFamily's behaviour.
			continue
		}
		observed, err := w.Fetch(ctx, e.Transport)
		if err != nil {
			// Cannot read from running — assume change broke
			// connectivity. Decline to confirm.
			return false
		}
		desired, hasDesired := res.Configuration[family]
		if !hasDesired {
			// Family in ManagedFamilies but absent from
			// Configuration: the writer would have nothing to
			// assert against. Treat as clean.
			continue
		}
		ops, err := w.Diff(desired, observed)
		if err != nil {
			return false
		}
		if len(ops) > 0 {
			// Drift between intent and running after a
			// committed merge — the device-side state diverged
			// from what we wrote, OR a side-effect rewrote part
			// of it. Either way, decline to confirm.
			return false
		}
	}
	return true
}

func (e *Engine) validate(res *intent.ResolvedIntent) error {
	if e.Transport == nil {
		return errors.New("Transport not configured")
	}
	if e.Lookup == nil {
		return errors.New("writer Lookup not configured")
	}
	if len(res.ManagedFamilies) == 0 {
		return errors.New("ManagedFamilies is empty")
	}
	if res.Configuration == nil {
		// An empty intent configuration is allowed — it means "no-op for
		// every managed family".
		return nil
	}
	return nil
}

// reconcileFamily runs Fetch → Diff → Apply → Verify for a single family.
// The function is intentionally verbose rather than pipelined so each
// stage's error is attributable in the returned FamilyStatus.
func (e *Engine) reconcileFamily(ctx context.Context, family string, res *intent.ResolvedIntent) FamilyStatus {
	w := e.Lookup(family)
	if w == nil {
		return FamilyStatus{
			Name: family, State: "Unsupported",
			Message: fmt.Sprintf("no writer registered for family %q", family),
		}
	}

	desired := res.Configuration[family]

	observed, err := w.Fetch(ctx, e.Transport)
	if err != nil {
		return FamilyStatus{
			Name: family, State: "ApplyError",
			Message: fmt.Sprintf("Fetch: %v", err),
		}
	}

	// Pull the family's slice out of the intent. Some families are nested
	// one level deeper (vlan.vlans, vrf.vrfs); writers accept the whole
	// family block and descend internally, so we pass the entire entry.
	ops, err := w.Diff(desired, observed)
	if err != nil {
		return FamilyStatus{
			Name: family, State: "ApplyError",
			Message: fmt.Sprintf("Diff: %v", err),
		}
	}
	// PruneOnRelinquish: opt-in DELETE ops for entries on the device
	// that the resolved intent no longer contains. Pruning runs only
	// when the CR opts in *and* the writer implements PruneCapable.
	// Writers that don't implement it are silently passed through —
	// every family will fail-closed by default until rolled out.
	if res.PruneOnRelinquish {
		if pc, ok := w.(writers.PruneCapable); ok {
			pruneOps, err := pc.PruneDiff(desired, observed)
			if err != nil {
				return FamilyStatus{
					Name: family, State: "ApplyError",
					Message: fmt.Sprintf("PruneDiff: %v", err),
				}
			}
			ops = append(ops, pruneOps...)
		}
	}

	// No-op: nothing to apply, nothing to verify. Family is InSync by
	// the writer's own definition of equivalence.
	if len(ops) == 0 {
		return FamilyStatus{Name: family, State: "InSync"}
	}

	// Report-policy short-circuit: we've observed drift, but the CR
	// opts into read-only mode. Surface it rather than applying.
	if res.DriftPolicy == configv1alpha1.DriftPolicyReport {
		return FamilyStatus{
			Name: family, State: "Drifted",
			OpCount: len(ops),
			Message: fmt.Sprintf("%d op(s) would be applied under driftPolicy=revert", len(ops)),
		}
	}

	applyStart := time.Now()
	// Apply through the engine's per-tick view of the transport. When
	// the tick is transactional this is the transactionalView wrapper
	// (writes go to the candidate datastore); otherwise it's the raw
	// transport. Falling back to e.Transport when applyTransport is
	// nil keeps direct callers of Engine.Reconcile that haven't gone
	// through Reconcile's setup (none in production, but unit tests
	// that wire reconcileFamily directly) working.
	at := e.applyTransport
	if at == nil {
		at = e.Transport
	}
	// Bump per-verb mutation-ops counter labelled by transport kind
	// before Apply runs. The intent is to attribute attempted ops to
	// the wire format the engine *intended* to use; Apply errors
	// then surface separately on cisco_vk_config_apply_errors_total
	// so live tests can distinguish "tried and failed" from "never
	// attempted." The transport label is the underlying e.Transport
	// kind (RESTCONF / NETCONF / gNMI), not the transactionalView
	// wrapper, so the counter still labels the wire correctly under
	// transactional reconciles.
	recordMutateOps(res.DeviceName, transportKindLabel(e.Transport), ops)
	if err := w.Apply(ctx, at, ops); err != nil {
		if applyDuration != nil {
			applyDuration.WithLabelValues(res.DeviceName, family).Observe(time.Since(applyStart).Seconds())
		}
		return FamilyStatus{
			Name: family, State: "ApplyError",
			OpCount: len(ops),
			Message: fmt.Sprintf("Apply: %v", err),
		}
	}
	if applyDuration != nil {
		applyDuration.WithLabelValues(res.DeviceName, family).Observe(time.Since(applyStart).Seconds())
	}

	// Verify: re-fetch and re-diff. Reads through the same view we
	// applied through (raw transport for non-transactional ticks,
	// candidate-bound view for transactional ticks). For NETCONF
	// candidate semantics this is the correct check: the candidate
	// reflects everything we just wrote, so the verify-diff exercises
	// the writer's own equivalence rules (idempotent diff) rather
	// than racing the device's running state. Post-commit drift —
	// where the device rejects part of the candidate at commit time
	// — surfaces on the NEXT reconcile tick because Commit's error
	// (caught at the engine level) flips Phase to Failed.
	verify, err := w.Fetch(ctx, at)
	if err != nil {
		return FamilyStatus{
			Name: family, State: "ApplyError",
			OpCount: len(ops),
			Message: fmt.Sprintf("Verify: re-fetch failed: %v", err),
		}
	}
	residual, err := w.Diff(desired, verify)
	if err != nil {
		return FamilyStatus{
			Name: family, State: "ApplyError",
			OpCount: len(ops),
			Message: fmt.Sprintf("Verify: re-diff failed: %v", err),
		}
	}
	// Verify against prune too — otherwise a pruned entry that the
	// device retained (e.g. a stale ACL the device kept around)
	// would slip past as InSync.
	if res.PruneOnRelinquish {
		if pc, ok := w.(writers.PruneCapable); ok {
			residualPrune, err := pc.PruneDiff(desired, verify)
			if err != nil {
				return FamilyStatus{
					Name: family, State: "ApplyError",
					OpCount: len(ops),
					Message: fmt.Sprintf("Verify: prune re-diff failed: %v", err),
				}
			}
			residual = append(residual, residualPrune...)
		}
	}
	if len(residual) > 0 {
		return FamilyStatus{
			Name: family, State: "Drifted",
			OpCount: len(ops),
			Message: fmt.Sprintf("%d op(s) applied but %d still pending", len(ops), len(residual)),
		}
	}
	return FamilyStatus{Name: family, State: "InSync", OpCount: len(ops)}
}

// ConflictCheck scans the IOSXEConfig set for the same device and
// reports ManagedFamily overlap. Phase-1 arbitration is advisory (the
// reconciler reports conflicts on status without refusing to apply);
// a Lease-based hard lock is a Phase-4 deliverable. Returned map is
// family → list of conflicting CR names; empty means no conflict.
func ConflictCheck(deviceName string, allForDevice []*configv1alpha1.IOSXEConfig) map[string][]string {
	seen := map[string][]string{}
	for _, cr := range allForDevice {
		if cr.Spec.DeviceRef.Name != deviceName {
			continue
		}
		for _, f := range cr.Spec.ManagedFamilies {
			seen[f] = append(seen[f], cr.Namespace+"/"+cr.Name)
		}
	}
	out := map[string][]string{}
	for family, owners := range seen {
		if len(owners) > 1 {
			out[family] = owners
		}
	}
	return out
}

// applyCLIBlock pushes one rendered CLI template to the device via
// the transport's VerbCLI op. One transport.Op per block so a
// single block's failure is attributed to that block in
// FamilyStatus without rolling back the preceding successful
// blocks — CLI templates are often idempotent CLI fragments, and
// "fix and retry this one" is the typical operator response.
//
// The FamilyStatus uses a "cli:<template>" namespace for the Name
// so external tooling (metrics, status consumers) can tell CLI
// blocks apart from netascode families in a single flat status
// list without adding another CR field.
func (e *Engine) applyCLIBlock(ctx context.Context, block intent.CLIBlock, res *intent.ResolvedIntent) FamilyStatus {
	famName := "cli:" + block.TemplateName
	op := transport.Op{
		Verb: transport.VerbCLI,
		Body: []byte(block.CLI),
	}
	applyStart := time.Now()
	// Wave 1A-fu (external-review-followup Finding #1): route CLI
	// ops through the engine's per-tick view of the transport so
	// they participate in the active transaction. Previously this
	// called e.Transport.Mutate directly, which always wrote
	// running config — bypassing the candidate datastore and
	// breaking the atomicity guarantee the transaction was
	// supposed to provide. The same applyTransport-or-Transport
	// fallback used in reconcileFamily applies here.
	at := e.applyTransport
	if at == nil {
		at = e.Transport
	}
	// Bump CLI-verb mutation counter labelled by transport kind
	// before issuing the wire call. CLI is a distinct verb from
	// the structured REPLACE/MERGE/DELETE so live tests can
	// detect whether a CLI block actually fired.
	recordMutateOps(res.DeviceName, transportKindLabel(e.Transport), []transport.Op{op})
	err := at.Mutate(ctx, "", []transport.Op{op})
	if applyDuration != nil {
		applyDuration.WithLabelValues(res.DeviceName, famName).Observe(time.Since(applyStart).Seconds())
	}
	if err != nil {
		return FamilyStatus{
			Name: famName, State: "ApplyError",
			OpCount: countCLILines(block.CLI),
			Message: fmt.Sprintf("Apply: %v", err),
		}
	}
	return FamilyStatus{
		Name:    famName,
		State:   "InSync",
		OpCount: countCLILines(block.CLI),
	}
}

// countCLILines returns the number of non-empty trimmed lines in
// a CLI block. Used for FamilyStatus.OpCount so the status UI
// shows a meaningful "blast radius" for CLI pushes even though
// each block is structurally a single transport.Op.
func countCLILines(cli string) int {
	n := 0
	for _, line := range strings.Split(cli, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
