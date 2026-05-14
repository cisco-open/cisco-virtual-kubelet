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
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/semconv"
	vktrace "github.com/virtual-kubelet/virtual-kubelet/trace"
)

// safeMsg formats an error message via fmt.Sprintf and runs the
// result through transport.RedactCredentials so credential-bearing
// payloads from device responses (`<password>`, `<key-string>`,
// `enable secret 5 ...`) never leak into IOSXEConfig.status,
// Kubernetes events, controller logs, or applylog entries. Used
// by every reconcileFamily error-return path that interpolates an
// error from a transport call.
//
// Wave 10 release-readiness fix (2026-04-28). Defense-in-depth: the
// transport layer also redacts at error-formation time; redacting
// here as well guarantees that even a future transport-side leak
// caught by neither path lands in the status writeback.
func safeMsg(format string, args ...any) string {
	return transport.RedactCredentials(fmt.Sprintf(format, args...))
}

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
	Lookup func(family, release string) writers.SectionWriter

	// DeviceVersion is the device-reported IOS-XE software release.
	// It is passed into Lookup so each writer instance captures the
	// correct per-device override resolver.
	DeviceVersion string

	// applyTransport is the transport handed to writers during the
	// apply loop. When the tick is non-transactional it equals
	// Transport. When the tick opens a transaction (NETCONF + spec
	// asks for it), apply-loop callsites swap in a transactionalView
	// so writers' Mutate calls land in the candidate datastore
	// instead of the running config. Reset before/after each tick;
	// not part of the public API.
	applyTransport transport.Interface

	// FamilyOrder is the optional ordering hook the engine applies
	// to res.ManagedFamilies before iterating per-family work. When
	// nil (the default), families are processed in the order the
	// resolver delivered them. When non-nil, it MUST return a
	// permutation of the input slice.
	//
	// Wave 10.3 — populated by iosxebuilder to a topological sort
	// over the schema's depends_on declarations, so atomic-replace
	// reconciles process parent families before dependent ones.
	// Tests pass an explicit ordering to assert the wiring.
	FamilyOrder func([]string) []string

	// RetryPolicy controls truncated exponential backoff applied to
	// idempotent transport calls (Fetch, Verify re-Fetch). Apply /
	// Mutate calls are NOT retried — non-transactional transports
	// (RESTCONF) cannot guarantee partial-application idempotency.
	// Zero-value uses transport.RetryPolicy{} defaults: 3 attempts,
	// 200ms initial, 2× growth, 2s cap, ±20% jitter.
	//
	// Wave 10 release-readiness fix (2026-04-28).
	RetryPolicy transport.RetryPolicy
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

	// AtomicReplaceOwnedKeys carries the per-family list-key values
	// this CR currently owns after this tick: the union of (prior
	// owned, set by ResolvedIntent.AtomicReplaceOwnedKeys) and (this
	// tick's desired keys, harvested from each writer's KeysOf hook
	// after a successful family apply). Entries successfully pruned
	// in this tick are removed. The reconciler writes this back to
	// IOSXEConfig.status.atomicReplaceOwnedKeys so the next tick
	// starts with up-to-date ownership.
	//
	// Only populated when AtomicReplace == true on the intent.
	// Writers that don't implement KeyExtractable contribute nothing
	// for their family — the engine falls back to the pre-Wave 10.3-
	// scope behaviour where every observed entry is candidate for
	// prune.
	//
	// Wave 10.3 scope refinement (2026-04-28).
	AtomicReplaceOwnedKeys map[string][]string

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

	// OwnedKeys carries the list-key values this CR owns for this
	// family after the tick. Populated only when the intent has
	// AtomicReplace == true AND the writer implements KeyExtractable
	// AND the apply (or no-op InSync) reflects a known desired set.
	// Aggregated by Reconcile into Result.AtomicReplaceOwnedKeys for
	// status writeback. Empty on Skipped / ApplyError so the
	// existing owned-set on the CR persists across failure ticks.
	//
	// Wave 10.3 scope refinement (2026-04-28).
	OwnedKeys []string
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
	ctx, span := vktrace.StartSpan(ctx, "cvk.config.reconcile")
	defer span.End()
	if res == nil {
		r := Result{Phase: PhaseFailed, Err: errors.New("engine.Reconcile: nil intent")}
		span.SetStatus(r.Err)
		recordResult("", r, time.Since(start).Seconds())
		return r
	}
	ctx = span.WithFields(ctx, map[string]any{
		"cisco.vk.device.name":   res.DeviceName,
		"cvk.config.families":    strings.Join(res.ManagedFamilies, ","),
		"cvk.config.driftPolicy": string(res.DriftPolicy),
		semconv.CvkEntityType:    semconv.EntityTypeConfig,
		semconv.CvkEntityID:      engineConfigEntityID(res),
		semconv.CvkEvidenceType:  semconv.EvidenceTypeConfigChange,
		semconv.CvkWorkflowName:  "config.apply",
		semconv.CvkTaskName:      "engine.reconcile",
	})

	if res.DriftPolicy == configv1alpha1.DriftPolicyPause {
		r := Result{Phase: PhasePaused, YangVersion: res.TargetYangVersion}
		recordResult(res.DeviceName, r, time.Since(start).Seconds())
		return r
	}

	if err := e.validate(res); err != nil {
		r := Result{Phase: PhaseFailed, Err: fmt.Errorf("Validating: %w", err), YangVersion: res.TargetYangVersion}
		span.SetStatus(r.Err)
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
			Phase:       PhaseFailed,
			Err:         ErrTransactionalCLIUnsupported,
			YangVersion: res.TargetYangVersion,
		}
		span.SetStatus(r.Err)
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
			recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "start_failed")
			r := Result{Phase: PhaseFailed, Err: fmt.Errorf("StartTransaction: %w", err), YangVersion: res.TargetYangVersion}
			span.SetStatus(r.Err)
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
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "discard")
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

	// Wave 10.3 — AtomicReplace=true treats the resolved intent as
	// the AUTHORITATIVE state for the managed families: device-side
	// entries not in the intent are deleted in the same transaction.
	// We achieve that by reusing the existing per-family
	// PruneOnRelinquish path (the writer's PruneCapable.PruneDiff
	// computes the delete-set), so atomic replace is strictly
	// stronger than pruneOnRelinquish — same per-family pruning,
	// PLUS the cross-family ordering applied below.
	//
	// Mutating the local res pointer is safe; ResolvedIntent is
	// constructed fresh per-tick by the resolver and the engine's
	// reconcile is the only caller that touches it.
	if res.AtomicReplace {
		res.PruneOnRelinquish = true
	}

	// Wave 10.3 — apply the cross-family ordering hook before the
	// per-family loop. iosxebuilder populates FamilyOrder with a
	// topological sort over schema/families.yaml's depends_on
	// declarations so adds run parent-first (e.g. VRF before any
	// interface that binds to it). Tests inject explicit ordering.
	// nil hook = identity (operator-determined order, the
	// pre-Wave-10 behaviour).
	families := res.ManagedFamilies
	if e.FamilyOrder != nil {
		families = e.FamilyOrder(families)
	}
	// Wave 10.3 scope refinement (2026-04-28): for atomic-replace
	// reconciles whose resolved intent is EMPTY (the canonical
	// delete-only case — test 09 phase 2, test 13 phase 2), REVERSE
	// the topological order so dependent families (e.g.
	// interface_loopback) are processed before their parents (e.g.
	// vrf). Forward order is correct for adds; reverse for deletes.
	//
	// Trigger only on "no managed family has any desired content"
	// because atomicReplace+non-empty intent is an add (or mixed)
	// reconcile and the forward-parent-first order is still right.
	// A mixed add+delete atomic-replace reconcile would benefit from
	// a finer-grained two-pass plan (forward-add, reverse-delete);
	// pure delete-only is the only shape this reversal handles.
	if res.AtomicReplace && len(families) > 1 && allFamiliesEmpty(res, families) {
		reversed := make([]string, len(families))
		for i, f := range families {
			reversed[len(families)-1-i] = f
		}
		families = reversed
	}
	for _, family := range families {
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
		// Wave 10.3 scope refinement: harvest the family's owned-keys
		// post-tick for the round-trip back to status. Skipped /
		// ApplyError families return empty OwnedKeys so the prior
		// status entry persists (the controller treats nil family
		// values as "no change"). Successful (InSync / Drifted)
		// families return the current desired's keys; the union with
		// the prior owned set is computed at status writeback time.
		if len(fs.OwnedKeys) > 0 {
			if result.AtomicReplaceOwnedKeys == nil {
				result.AtomicReplaceOwnedKeys = map[string][]string{}
			}
			result.AtomicReplaceOwnedKeys[family] = fs.OwnedKeys
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
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "commit_failed")
			} else if !e.runningVerify(ctx, res) {
				// running-Verify failed: do NOT call ConfirmCommit.
				// Deferred Discard cleans up the candidate lock; the
				// device's confirm-timeout timer fires and reverts
				// running to pre-commit. txCommitted stays false so
				// the deferred Discard runs.
				result.Phase = PhaseFailed
				result.Err = errors.New("running-verify failed after CommitConfirmed; device will auto-revert at confirm-timeout")
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "auto_reverted")
			} else if err := cc.ConfirmCommit(ctx); err != nil {
				// CommitConfirmed succeeded but the follow-up
				// confirm RPC failed. The device will auto-revert
				// at the timeout — this is the safe failure mode.
				result.Phase = PhaseFailed
				result.Err = fmt.Errorf("ConfirmCommit: %w", err)
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "auto_reverted")
			} else {
				txCommitted = true
				result.ConfirmedCommitUsed = true
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "confirmed")
			}
		} else {
			result.ConfirmedCommitFallback = fallbackReason
			if err := e.Transport.Commit(ctx, txHandle); err != nil {
				result.Phase = PhaseFailed
				result.Err = fmt.Errorf("Commit: %w", err)
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "commit_failed")
			} else {
				txCommitted = true
				recordTransaction(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "commit")
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
			recordSaveStartup(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "failed")
		} else {
			result.SaveStartupOK = true
			recordSaveStartup(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), "ok")
		}
	}

	recordResult(res.DeviceName, result, time.Since(start).Seconds())
	span.SetStatus(result.Err)
	return result
}

func engineConfigEntityID(res *intent.ResolvedIntent) string {
	if res == nil {
		return ""
	}
	if res.SourceCR != nil {
		if res.SourceCR.UID != "" {
			return string(res.SourceCR.UID)
		}
		if res.SourceCR.Namespace != "" {
			return res.SourceCR.Namespace + "/" + res.SourceCR.Name
		}
		if res.SourceCR.Name != "" {
			return res.SourceCR.Name
		}
	}
	return res.DeviceName
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
		w := e.lookupWriter(family)
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
	ctx, familySpan := vktrace.StartSpan(ctx, "cvk.config.family")
	ctx = familySpan.WithFields(ctx, map[string]any{
		"cisco.vk.device.name":  res.DeviceName,
		"cvk.config.family":     family,
		semconv.CvkEntityType:   semconv.EntityTypeConfig,
		semconv.CvkEntityID:     engineConfigEntityID(res),
		semconv.CvkEvidenceType: semconv.EvidenceTypeConfigChange,
		semconv.CvkWorkflowName: "config.apply",
		semconv.CvkTaskName:     "family.reconcile",
	})
	defer familySpan.End()
	w := e.lookupWriter(family)
	if w == nil {
		return FamilyStatus{
			Name: family, State: "Unsupported",
			Message: safeMsg("no writer registered for family %q", family),
		}
	}

	desired := res.Configuration[family]

	// Wave 10.3 scope refinement: precompute the desired-side keys
	// once so successful-return paths can stamp them onto the
	// FamilyStatus. The aggregator in Reconcile unions these with the
	// prior owned set to produce Result.AtomicReplaceOwnedKeys.
	//
	// Track on every reconcile (regardless of res.AtomicReplace) so
	// flipping atomicReplace from false → true on a CR's next
	// generation already has a populated owned-set to scope against.
	// Without this, a 2-phase test that establishes state with
	// atomicReplace=false then flips it to true on phase 2 would
	// see an empty priorOwned set and prune nothing — exactly the
	// shape of release-blocker tests 09 + 13.
	var ownedKeysForFamily []string
	if ke, ok := w.(writers.KeyExtractable); ok {
		ownedKeysForFamily = ke.KeysOf(desired)
	}

	// Wave 10 release-readiness: Fetch is idempotent (read-only) so
	// transient TCP-level errors (connection refused, i/o timeout)
	// retry per the engine's RetryPolicy. Application errors
	// (rpc-error, access-denied) are NOT retried — they bubble
	// straight up. See transport.IsTransient for the matcher.
	var observed any
	planCtx, planSpan := vktrace.StartSpan(ctx, "cvk.config.plan")
	planCtx = planSpan.WithFields(planCtx, map[string]any{
		"cvk.config.phase":      "fetch-diff",
		semconv.CvkEntityType:   semconv.EntityTypeConfig,
		semconv.CvkEntityID:     engineConfigEntityID(res),
		semconv.CvkEvidenceType: semconv.EvidenceTypeConfigChange,
		semconv.CvkWorkflowName: "config.apply",
		semconv.CvkTaskName:     "plan.diff",
	})
	err := transport.RetryIdempotent(planCtx, e.RetryPolicy, func() error {
		var ferr error
		observed, ferr = w.Fetch(planCtx, e.Transport)
		return ferr
	})
	if err != nil {
		planSpan.SetStatus(err)
		planSpan.End()
		familySpan.SetStatus(err)
		return FamilyStatus{
			Name: family, State: "ApplyError",
			Message: safeMsg("Fetch: %v", err),
		}
	}

	// Pull the family's slice out of the intent. Some families are nested
	// one level deeper (vlan.vlans, vrf.vrfs); writers accept the whole
	// family block and descend internally, so we pass the entire entry.
	ops, err := w.Diff(desired, observed)
	if err != nil {
		planSpan.SetStatus(err)
		planSpan.End()
		familySpan.SetStatus(err)
		return FamilyStatus{
			Name: family, State: "ApplyError",
			Message: safeMsg("Diff: %v", err),
		}
	}
	// PruneOnRelinquish: opt-in DELETE ops for entries on the device
	// that the resolved intent no longer contains. Pruning runs only
	// when the CR opts in *and* the writer implements PruneCapable.
	// Writers that don't implement it are silently passed through —
	// every family will fail-closed by default until rolled out.
	if res.PruneOnRelinquish {
		if pc, ok := w.(writers.PruneCapable); ok {
			// Scope the prune set to entries this CR has owned
			// (status.atomicReplaceOwnedKeys) UNION current desired.
			// Anything observed outside that scope is baseline state
			// the CR has never touched and must not be deleted —
			// e.g. operator-authored Loopback 0, Mgmt-vrf, default
			// VLANs that pre-existed on the device.
			//
			// Originally Wave 10.3 only applied this scoping under
			// atomicReplace, but live validation (2026-05-01) showed
			// pruneOnRelinquish without atomicReplace silently wipes
			// shared baseline state on the next reconcile. ownedKeys
			// are tracked on every reconcile regardless of
			// res.AtomicReplace (see ownedKeysForFamily above), so
			// the scoping is safe to apply universally.
			//
			// A3 fix (2026-05-01): when a PruneCapable writer does
			// not implement KeyExtractable we cannot scope the prune
			// safely. Pre-fix the engine silently skipped prune in
			// that case and reported InSync — operators had no way
			// to know prune never ran. Surface this as a hard
			// Unsupported error so pruneOnRelinquish either runs
			// scoped or fails loudly. Adding KeysOf to a writer is
			// the documented uplift path; see writers/dhcp.go for
			// the canonical shape.
			ke, isKE := w.(writers.KeyExtractable)
			if !isKE {
				err := fmt.Errorf("family %s is PruneCapable but not KeyExtractable", family)
				planSpan.SetStatus(err)
				planSpan.End()
				familySpan.SetStatus(err)
				return FamilyStatus{
					Name: family, State: "Unsupported",
					Message: safeMsg(
						"family %s is PruneCapable but not KeyExtractable; "+
							"pruneOnRelinquish requires both so deletes can be "+
							"scoped to CR-owned keys. Add KeysOf to the writer "+
							"or remove pruneOnRelinquish from the CR.", family),
				}
			}
			pruneInput := scopeObservedToOwned(ke, observed, desired, res.AtomicReplaceOwnedKeys[family])
			pruneOps, err := pc.PruneDiff(desired, pruneInput)
			if err != nil {
				planSpan.SetStatus(err)
				planSpan.End()
				familySpan.SetStatus(err)
				return FamilyStatus{
					Name: family, State: "ApplyError",
					Message: safeMsg("PruneDiff: %v", err),
				}
			}
			ops = append(ops, pruneOps...)
		}
	}
	planSpan.End()

	// No-op: nothing to apply, nothing to verify. Family is InSync by
	// the writer's own definition of equivalence.
	if len(ops) == 0 {
		return FamilyStatus{Name: family, State: "InSync", OwnedKeys: ownedKeysForFamily}
	}

	// Report-policy short-circuit: we've observed drift, but the CR
	// opts into read-only mode. Surface it rather than applying.
	if res.DriftPolicy == configv1alpha1.DriftPolicyReport {
		return FamilyStatus{
			Name: family, State: "Drifted",
			OpCount: len(ops),
			Message: safeMsg("driftPolicy=report: %d op(s) detected as drift but not applied; switch to driftPolicy=revert to reconcile", len(ops)),
		}
	}

	applyStart := time.Now()
	applyCtx, applySpan := vktrace.StartSpan(ctx, "cvk.config.apply")
	applyCtx = applySpan.WithFields(applyCtx, map[string]any{
		"cvk.config.family":     family,
		"cvk.config.op_count":   len(ops),
		semconv.CvkEntityType:   semconv.EntityTypeConfig,
		semconv.CvkEntityID:     engineConfigEntityID(res),
		semconv.CvkEvidenceType: semconv.EvidenceTypeConfigChange,
		semconv.CvkWorkflowName: "config.apply",
		semconv.CvkTaskName:     "apply.diff",
	})
	defer applySpan.End()
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
	recordMutateOps(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), ops)
	if err := w.Apply(applyCtx, at, ops); err != nil {
		if applyDuration != nil {
			applyDuration.WithLabelValues(res.DeviceName, releaseLabel(res.TargetYangVersion), family).Observe(time.Since(applyStart).Seconds())
		}
		applySpan.SetStatus(err)
		familySpan.SetStatus(err)
		return FamilyStatus{
			Name: family, State: "ApplyError",
			OpCount: len(ops),
			Message: safeMsg("Apply: %v", err),
		}
	}
	if applyDuration != nil {
		applyDuration.WithLabelValues(res.DeviceName, releaseLabel(res.TargetYangVersion), family).Observe(time.Since(applyStart).Seconds())
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
	// Wave 10 release-readiness: Verify re-Fetch is idempotent;
	// retry transient TCP-level errors per RetryPolicy.
	var verify any
	verifyCtx, verifySpan := vktrace.StartSpan(ctx, "cvk.config.verify")
	verifyCtx = verifySpan.WithFields(verifyCtx, map[string]any{
		"cvk.config.family":     family,
		semconv.CvkEntityType:   semconv.EntityTypeConfig,
		semconv.CvkEntityID:     engineConfigEntityID(res),
		semconv.CvkEvidenceType: semconv.EvidenceTypeConfigChange,
		semconv.CvkWorkflowName: "config.apply",
		semconv.CvkTaskName:     "verify.in_sync",
	})
	defer verifySpan.End()
	err = transport.RetryIdempotent(verifyCtx, e.RetryPolicy, func() error {
		var ferr error
		verify, ferr = w.Fetch(verifyCtx, at)
		return ferr
	})
	if err != nil {
		verifySpan.SetStatus(err)
		familySpan.SetStatus(err)
		return FamilyStatus{
			Name: family, State: "ApplyError",
			OpCount: len(ops),
			Message: safeMsg("Verify: re-fetch failed: %v", err),
		}
	}
	residual, err := w.Diff(desired, verify)
	if err != nil {
		verifySpan.SetStatus(err)
		familySpan.SetStatus(err)
		return FamilyStatus{
			Name: family, State: "ApplyError",
			OpCount: len(ops),
			Message: safeMsg("Verify: re-diff failed: %v", err),
		}
	}
	// Verify against prune too — otherwise a pruned entry that the
	// device retained (e.g. a stale ACL the device kept around)
	// would slip past as InSync.
	if res.PruneOnRelinquish {
		if pc, ok := w.(writers.PruneCapable); ok {
			// Mirror the apply-path scoping. The not-KeyExtractable
			// branch is unreachable here in practice — the apply-
			// path Unsupported short-circuit returns before we get
			// to verify — but we keep a defensive skip just in case
			// the family roster shifts mid-reconcile.
			if ke, ok := w.(writers.KeyExtractable); ok {
				vInput := scopeObservedToOwned(ke, verify, desired, res.AtomicReplaceOwnedKeys[family])
				residualPrune, err := pc.PruneDiff(desired, vInput)
				if err != nil {
					verifySpan.SetStatus(err)
					familySpan.SetStatus(err)
					return FamilyStatus{
						Name: family, State: "ApplyError",
						OpCount: len(ops),
						Message: safeMsg("Verify: prune re-diff failed: %v", err),
					}
				}
				residual = append(residual, residualPrune...)
			}
		}
	}
	if len(residual) > 0 {
		verifySpan.SetStatus(errors.New("residual drift after apply"))
		return FamilyStatus{
			Name: family, State: "Drifted",
			OpCount:   len(ops),
			Message:   fmt.Sprintf("%d op(s) applied but %d still pending", len(ops), len(residual)),
			OwnedKeys: ownedKeysForFamily,
		}
	}
	return FamilyStatus{Name: family, State: "InSync", OpCount: len(ops), OwnedKeys: ownedKeysForFamily}
}

func (e *Engine) lookupWriter(family string) writers.SectionWriter {
	if e.Lookup == nil {
		return nil
	}
	return e.Lookup(family, e.DeviceVersion)
}

// allFamiliesEmpty reports whether every family in `families` has no
// desired content under res.Configuration. Used to detect the
// atomic-replace delete-only case, where the engine reverses family
// order so child families are processed before their parents (e.g.
// loopback before vrf). Returns true on a nil or empty Configuration
// map, OR when every per-family entry is nil / has no recognised
// inner list.
//
// "Recognised" inner-list shapes: a map containing at least one key
// whose value is a non-empty slice. The check is intentionally
// loose — a writer can interpret its block in family-specific ways,
// but every Phase-1 family writer's "empty intent" shape resolves to
// either a nil block or a block whose inner list is an empty slice.
func allFamiliesEmpty(res *intent.ResolvedIntent, families []string) bool {
	if len(res.Configuration) == 0 {
		return true
	}
	for _, fam := range families {
		v := res.Configuration[fam]
		if v == nil {
			continue
		}
		switch tv := v.(type) {
		case map[string]any:
			for _, inner := range tv {
				switch iv := inner.(type) {
				case []any:
					if len(iv) > 0 {
						return false
					}
				case []map[string]any:
					if len(iv) > 0 {
						return false
					}
				}
			}
		case []any:
			if len(tv) > 0 {
				return false
			}
		}
	}
	return true
}

// scopeObservedToOwned filters an observed list (writer-opaque shape)
// down to entries whose key is in (priorOwned ∪ desired). Used by the
// atomic-replace prune phase so the engine never tries to delete
// device-side entries this CR has not previously touched.
//
// The filter relies on the writer's KeyExtractable hook to enumerate
// keys both for desired and observed. We rebuild a stub-list shape
// (one map per kept entry, keyed by `name` / `id` / `sequence` per
// the writer's expectations) and pass that to PruneDiff. Because the
// stub list contains only the keys, PruneDiff's "anything in observed
// not in desired → DELETE" logic produces deletes only for owned
// entries that aren't in current desired — which is exactly the
// scoped-atomic-replace semantic.
//
// The function is conservative on shape mismatches: if KeysOf returns
// nil for either side, we fall through to the un-filtered observed
// (the pre-Wave-10.3 behaviour). That keeps the engine safe against
// writers that haven't yet implemented KeyExtractable.
//
// Wave 10.3 scope refinement (2026-04-28).
func scopeObservedToOwned(ke writers.KeyExtractable, observed, desired any, priorOwned []string) any {
	observedKeys := ke.KeysOf(observed)
	if len(observedKeys) == 0 {
		return observed
	}
	desiredKeys := ke.KeysOf(desired)
	allowed := make(map[string]struct{}, len(priorOwned)+len(desiredKeys))
	for _, k := range priorOwned {
		allowed[k] = struct{}{}
	}
	for _, k := range desiredKeys {
		allowed[k] = struct{}{}
	}
	// Rebuild a minimal-shape observed list with only the allowed
	// keys. The writer's PruneDiff accepts the bare-list shape
	// (coerceList handles both block and bare list). We can't
	// project the entry's full body — we only have the keys — but
	// PruneDiff only needs the key field to compute deletes.
	//
	// For the keyedListWriter family the key field is `name` / `id`
	// / `sequence`. interface_ethernet uses composite `<type>=<name>`
	// and we synthesise a two-field map. The KeysOf string we get
	// back is `<type>=<name>` so we split on the first `=`.
	stubs := make([]map[string]any, 0, len(observedKeys))
	observedSeen := map[string]struct{}{}
	for _, k := range observedKeys {
		if _, ok := allowed[k]; !ok {
			continue
		}
		if _, dup := observedSeen[k]; dup {
			continue
		}
		observedSeen[k] = struct{}{}
		stubs = append(stubs, stubEntryFromKey(k))
	}
	return stubs
}

// stubEntryFromKey reconstructs a minimal map[string]any from a key
// string produced by writer KeysOf hooks. Two shapes are recognised:
//
//	"GigabitEthernet=0/0/0"    → {type: GigabitEthernet, name: 0/0/0}
//	"<scalar>"                 → {name: <scalar>, id: <scalar>,
//	                              sequence: <scalar>}
//
// The latter populates all three common key-field names so that
// keyedListWriter.PruneDiff finds the right one regardless of which
// the family's keyField is set to. A real entry would have only one;
// the duplicates are harmless because the entry is never written
// back to the device — it's solely a vehicle for PruneDiff to compute
// the delete set.
func stubEntryFromKey(k string) map[string]any {
	if i := strings.Index(k, "="); i > 0 {
		return map[string]any{"type": k[:i], "name": k[i+1:]}
	}
	return map[string]any{"name": k, "id": k, "sequence": k}
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
	ctx, span := vktrace.StartSpan(ctx, "cvk.config.apply")
	ctx = span.WithFields(ctx, map[string]any{
		"cisco.vk.device.name":  res.DeviceName,
		"cvk.config.family":     famName,
		"cvk.config.kind":       "cli",
		semconv.CvkEntityType:   semconv.EntityTypeConfig,
		semconv.CvkEntityID:     engineConfigEntityID(res),
		semconv.CvkEvidenceType: semconv.EvidenceTypeConfigChange,
		semconv.CvkWorkflowName: "config.apply",
		semconv.CvkTaskName:     "apply.cli_block",
	})
	defer span.End()
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
	recordMutateOps(res.DeviceName, res.TargetYangVersion, transportKindLabel(e.Transport), []transport.Op{op})
	err := at.Mutate(ctx, "", []transport.Op{op})
	if applyDuration != nil {
		applyDuration.WithLabelValues(res.DeviceName, releaseLabel(res.TargetYangVersion), famName).Observe(time.Since(applyStart).Seconds())
	}
	if err != nil {
		span.SetStatus(err)
		return FamilyStatus{
			Name: famName, State: "ApplyError",
			OpCount: countCLILines(block.CLI),
			Message: safeMsg("Apply: %v", err),
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
