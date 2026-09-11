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

// Package mutationguard provides the one-release compatibility barrier for
// disruptive operation objects created before the shared mutation Lease and
// durable software-upgrade execution marker existed.
package mutationguard

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
)

// LegacyIOSXEQuarantineTTL is the released IOS XE compatibility horizon. It
// is deliberately independent of the calling driver's normal Lease TTL so a
// future driver cannot shorten an IOS XE mutation fence accidentally.
const LegacyIOSXEQuarantineTTL = 26 * time.Hour

// RenewalInterval bounds compatibility heartbeats without turning a long
// quarantine into high-rate API traffic.
func RenewalInterval(ttl time.Duration) time.Duration {
	interval := ttl / 2
	if interval > 5*time.Minute {
		interval = 5 * time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}
	return interval
}

// Risk identifies one operation whose device-side outcome cannot be inferred
// safely from status written by a released or newer controller.
type Risk struct {
	Kind           string
	Namespace      string
	Name           string
	HolderIdentity string
	LeaseTTL       time.Duration
}

func (r Risk) String() string {
	return fmt.Sprintf("%s %s/%s", r.Kind, r.Namespace, r.Name)
}

// Result describes the canonical risk and the Lease acquired on its behalf.
// CallerOwnsRisk is true only when the caller is itself the canonical risky
// object and may continue its fail-closed recovery path.
type Result struct {
	Risk           *Risk
	LeaseOwned     bool
	CallerOwnsRisk bool
	ExistingHolder string
}

// EnsureCanonicalQuarantine finds the same canonical risky object for every
// controller and acquires or renews the shared Lease using that object's
// identity. API discovery failures are returned so callers fail closed.
func EnsureCanonicalQuarantine(
	ctx context.Context,
	reader client.Reader,
	leaser *engine.FamilyLeaser,
	namespace string,
	deviceName string,
	deviceKey string,
	callerIdentity string,
	now time.Time,
) (Result, error) {
	return ensureCanonicalQuarantine(ctx, reader, leaser, namespace, deviceName, deviceKey, callerIdentity, now)
}

// EnsureCanonicalQuarantineForDeletion establishes the same quarantine while
// a risky object is being deleted. It may replace an expired foreign holder,
// but only after that holder's complete Lease TTL has elapsed; this preserves
// liveness without creating any authority to issue a device mutation.
func EnsureCanonicalQuarantineForDeletion(
	ctx context.Context,
	reader client.Reader,
	leaser *engine.FamilyLeaser,
	namespace string,
	deviceName string,
	deviceKey string,
	callerIdentity string,
	now time.Time,
) (Result, error) {
	return ensureCanonicalQuarantine(ctx, reader, leaser, namespace, deviceName, deviceKey, callerIdentity, now)
}

func ensureCanonicalQuarantine(
	ctx context.Context,
	reader client.Reader,
	leaser *engine.FamilyLeaser,
	namespace string,
	deviceName string,
	deviceKey string,
	callerIdentity string,
	now time.Time,
) (Result, error) {
	risk, err := FindCanonicalRisk(ctx, reader, namespace, deviceName, now)
	if err != nil {
		return Result{}, err
	}
	if risk == nil {
		return Result{}, nil
	}
	if leaser == nil {
		return Result{Risk: risk}, fmt.Errorf("mutation leaser is not configured while %s requires quarantine", risk)
	}

	guardLeaser := *leaser
	if risk.LeaseTTL > 0 {
		guardLeaser.TTL = risk.LeaseTTL
	}
	leaseResult, err := guardLeaser.Acquire(ctx, deviceKey, devicecoordination.MutationLeaseFamily, risk.HolderIdentity)
	if err != nil {
		return Result{Risk: risk}, fmt.Errorf("acquire canonical quarantine for %s: %w", risk, err)
	}
	return Result{
		Risk:           risk,
		LeaseOwned:     leaseResult.Owned,
		CallerOwnsRisk: leaseResult.Owned && callerIdentity == risk.HolderIdentity,
		ExistingHolder: leaseResult.Holder,
	}, nil
}

// FindCanonicalRisk lists both mutation CRDs in one namespace and returns a
// deterministic holder. Deleting objects remain visible until their finalizer
// is resolved and therefore continue to participate in this barrier.
func FindCanonicalRisk(
	ctx context.Context,
	reader client.Reader,
	namespace, deviceName string,
	now time.Time,
) (*Risk, error) {
	if reader == nil {
		return nil, fmt.Errorf("legacy mutation guard requires an API reader")
	}
	var risks []Risk

	var upgrades opsv1alpha1.IOSXESoftwareUpgradeList
	if err := reader.List(ctx, &upgrades, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list IOSXESoftwareUpgrade compatibility risks: %w", err)
	}
	for i := range upgrades.Items {
		up := &upgrades.Items[i]
		if up.Spec.DeviceRef.Name != deviceName {
			continue
		}
		leaseTTL, risky := upgradeQuarantineTTL(up, now)
		if !risky {
			continue
		}
		risks = append(risks, Risk{
			Kind:           "IOSXESoftwareUpgrade",
			Namespace:      up.Namespace,
			Name:           up.Name,
			HolderIdentity: UpgradeHolderIdentity(up),
			LeaseTTL:       leaseTTL,
		})
	}

	var actions opsv1alpha1.IOSXEOperationalActionList
	if err := reader.List(ctx, &actions, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list IOSXEOperationalAction compatibility risks: %w", err)
	}
	for i := range actions.Items {
		act := &actions.Items[i]
		if act.Spec.DeviceRef.Name != deviceName {
			continue
		}
		leaseTTL, risky := actionQuarantineTTL(act, now)
		if !risky {
			continue
		}
		risks = append(risks, Risk{
			Kind:           "IOSXEOperationalAction",
			Namespace:      act.Namespace,
			Name:           act.Name,
			HolderIdentity: ActionHolderIdentity(act),
			LeaseTTL:       leaseTTL,
		})
	}

	if len(risks) == 0 {
		return nil, nil
	}
	sort.Slice(risks, func(i, j int) bool {
		if risks[i].Kind != risks[j].Kind {
			return risks[i].Kind < risks[j].Kind
		}
		if risks[i].Namespace != risks[j].Namespace {
			return risks[i].Namespace < risks[j].Namespace
		}
		if risks[i].Name != risks[j].Name {
			return risks[i].Name < risks[j].Name
		}
		return risks[i].HolderIdentity < risks[j].HolderIdentity
	})
	return &risks[0], nil
}

// UpgradeHolderIdentity is shared with the software-upgrade reconciler so a
// guard acting on its behalf uses exactly the same Lease owner.
func UpgradeHolderIdentity(up *opsv1alpha1.IOSXESoftwareUpgrade) string {
	if up == nil {
		return ""
	}
	return devicecoordination.HolderIdentity("software-upgrade", up.Namespace, up.Name, string(up.UID))
}

// ActionHolderIdentity is shared with the operational-action reconciler so a
// guard acting on its behalf uses exactly the same Lease owner.
func ActionHolderIdentity(act *opsv1alpha1.IOSXEOperationalAction) string {
	if act == nil {
		return ""
	}
	return devicecoordination.HolderIdentity("operational-action", act.Namespace, act.Name, string(act.UID))
}

// UpgradeRequiresQuarantineAt reports whether a released or newer upgrade can
// still overlap another mutation and must participate in the shared barrier.
func UpgradeRequiresQuarantineAt(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) bool {
	_, risky := upgradeQuarantineTTL(up, now)
	return risky
}

func upgradeQuarantineTTL(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (time.Duration, bool) {
	if up == nil {
		return 0, false
	}
	if up.Status.ExecutionModel != "" &&
		up.Status.ExecutionModel != opsv1alpha1.UpgradeExecutionModelAtMostOnceV1 {
		return LegacyIOSXEQuarantineTTL, true
	}
	if up.Status.ExecutionModel != "" {
		return 0, false
	}
	switch up.Status.Phase {
	case "", opsv1alpha1.UpgradePhasePending, opsv1alpha1.UpgradePhaseResolving,
		opsv1alpha1.UpgradePhaseSucceeded,
		opsv1alpha1.UpgradePhaseStagedForNextBoot,
		opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseCancelled:
		return 0, false
	case opsv1alpha1.UpgradePhaseFailed,
		opsv1alpha1.UpgradePhaseValidationFailed,
		opsv1alpha1.UpgradePhaseRebootTimeout:
		return remainingUpgradeQuarantine(up, now, LegacyIOSXEQuarantineTTL)
	default:
		// Every other markerless phase is unsafe by default. This includes the
		// released in-flight phases and phases unknown to this controller; an
		// absent execution marker cannot prove that device work was not sent.
		return LegacyIOSXEQuarantineTTL, true
	}
}

func remainingUpgradeQuarantine(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
	duration time.Duration,
) (time.Duration, bool) {
	anchor := up.CreationTimestamp.Time
	for _, candidate := range []*metav1.Time{
		up.Status.StartTime,
		up.Status.InstallStartTime,
		up.Status.ActivationStartTime,
		up.Status.RollbackStartTime,
		up.Status.CompletionTime,
	} {
		if candidate != nil && candidate.Time.After(anchor) {
			anchor = candidate.Time
		}
	}
	if anchor.IsZero() {
		// A terminal marker without a trustworthy timestamp cannot be aged out
		// safely. Renew a bounded fence whenever another mutation is tried.
		return duration, true
	}
	remaining := anchor.Add(duration).Sub(now)
	if remaining <= 0 {
		return 0, false
	}
	if remaining > duration {
		remaining = duration
	}
	return remaining, true
}

// ActionRequiresQuarantine recognizes the released pre-Lease Running state.
func ActionRequiresQuarantine(act *opsv1alpha1.IOSXEOperationalAction) bool {
	return act != nil && act.Spec.Action.Kind != opsv1alpha1.ActionKindCancelReboot &&
		act.Status.Phase == opsv1alpha1.ActionPhaseRunning && act.Status.InvocationID != ""
}

// ActionRequiresQuarantineAt reports whether a released pre-Lease action can
// still overlap another mutation and must participate in the shared barrier.
func ActionRequiresQuarantineAt(
	act *opsv1alpha1.IOSXEOperationalAction,
	now time.Time,
) bool {
	_, risky := actionQuarantineTTL(act, now)
	return risky
}

func actionQuarantineTTL(
	act *opsv1alpha1.IOSXEOperationalAction,
	now time.Time,
) (time.Duration, bool) {
	if act == nil || act.Spec.Action.Kind == opsv1alpha1.ActionKindCancelReboot || act.Status.InvocationID == "" {
		return 0, false
	}
	delay := legacyRebootDelay(act)
	switch act.Status.Phase {
	case opsv1alpha1.ActionPhaseRunning:
		return LegacyIOSXEQuarantineTTL + delay, true
	case opsv1alpha1.ActionPhaseFailed:
		return remainingTerminalQuarantine(act, now, LegacyIOSXEQuarantineTTL+delay)
	case opsv1alpha1.ActionPhaseSucceeded:
		switch act.Spec.Action.Kind {
		case opsv1alpha1.ActionKindReboot:
			return remainingTerminalQuarantine(act, now, LegacyIOSXEQuarantineTTL+delay)
		case opsv1alpha1.ActionKindFactoryReset:
			return remainingTerminalQuarantine(act, now, LegacyIOSXEQuarantineTTL)
		}
	}
	return 0, false
}

func remainingTerminalQuarantine(
	act *opsv1alpha1.IOSXEOperationalAction,
	now time.Time,
	duration time.Duration,
) (time.Duration, bool) {
	anchor := act.CreationTimestamp.Time
	if act.Status.StartTime != nil && act.Status.StartTime.Time.After(anchor) {
		anchor = act.Status.StartTime.Time
	}
	if act.Status.CompletionTime != nil && act.Status.CompletionTime.Time.After(anchor) {
		anchor = act.Status.CompletionTime.Time
	}
	if anchor.IsZero() {
		// An invocation marker without a trustworthy timestamp cannot be aged
		// out safely. Renew a bounded fence whenever another mutation is tried.
		return duration, true
	}
	remaining := anchor.Add(duration).Sub(now)
	if remaining <= 0 {
		return 0, false
	}
	if remaining > duration {
		remaining = duration
	}
	return remaining, true
}

func legacyRebootDelay(act *opsv1alpha1.IOSXEOperationalAction) time.Duration {
	if act == nil || act.Spec.Action.Kind != opsv1alpha1.ActionKindReboot || act.Spec.Action.Reboot == nil {
		return 0
	}
	seconds := act.Spec.Action.Reboot.DelaySeconds
	if seconds <= 0 {
		return 0
	}
	// Released controllers rejected values above the CRD's seven-day limit
	// before persisting InvocationID, so this cap is both overflow-safe and an
	// exact bound on a delayed reboot they could have dispatched.
	const maxLegacyDelaySeconds int64 = 7 * 24 * 60 * 60
	if seconds > maxLegacyDelaySeconds {
		seconds = maxLegacyDelaySeconds
	}
	return time.Duration(seconds) * time.Second
}
