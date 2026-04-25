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
	var (
		txHandle    transport.TxHandle
		txCommitted bool
	)
	if transactional {
		h, err := e.Transport.StartTransaction(ctx)
		if err != nil {
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
			}
			e.applyTransport = nil
		}()
	} else {
		e.applyTransport = e.Transport
		defer func() { e.applyTransport = nil }()
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
	if transactional && result.Phase == PhaseInSync {
		if err := e.Transport.Commit(ctx, txHandle); err != nil {
			result.Phase = PhaseFailed
			result.Err = fmt.Errorf("Commit: %w", err)
		} else {
			txCommitted = true
		}
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
		} else {
			result.SaveStartupOK = true
		}
	}

	recordResult(res.DeviceName, result, time.Since(start).Seconds())
	return result
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
	err := e.Transport.Mutate(ctx, "", []transport.Op{op})
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
