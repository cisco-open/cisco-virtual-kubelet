// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The verifier must establish rendered-device semantics for this exact intent
// and complete admitted population. Successful commands or native task/readback
// alone cannot provide that proof. No public/default verifier is installed here.
type sdaTransitConvergenceProof func(context.Context, sdaTransitIntent, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time, map[string]sdaTransitDeviceEvidence) error

type sdaTransitConvergenceObservation struct {
	native    *sdaTransitSnapshot
	devices   map[string]sdaTransitDeviceEvidence
	completed time.Time
}

func (sdaTransitConvergenceObservation) String() string {
	return "<private SDA convergence observation>"
}
func (sdaTransitConvergenceObservation) GoString() string {
	return "<private SDA convergence observation>"
}

func copySDATransitDeviceEvidence(in map[string]sdaTransitDeviceEvidence) map[string]sdaTransitDeviceEvidence {
	out := make(map[string]sdaTransitDeviceEvidence, len(in))
	for id, e := range in {
		out[id] = sdaTransitDeviceEvidence{outputs: append([]string(nil), e.outputs...)}
	}
	return out
}

// Bind an application to the logical definition, actual native incarnation and
// canonical physical population. This is completion evidence, not exclusive
// creator ownership or authority to adopt, replace or delete the native object.
func sdaTransitApplicationKey(want sdaTransitIntent, s *sdaTransitSnapshot, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if s == nil || s.actual == nil || !validAPIID(s.actual.ID) || op == nil || op.Section != "fabric" || op.Phase != "Verifying" || op.Action != "Create" || contract.ValidateGroupJournalIdentity(op) != nil || sdaTransitResource(s) != op.Resource || s.digest != op.TargetID || !sdaTransitMatches(s.native, *s.actual) || want.name != s.native.Name || want.kind != s.native.Type {
		return "", errConfigurationStale
	}
	targets, err := contract.CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal([]any{"v1", want.name, want.kind, s.actual.ID, targets})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sda-transit:%x", sha256.Sum256(raw)), nil
}

// Private completion bridge to the existing atomic group receipt protocol.
// It submits nothing and changes no member, lease or finalizer. Public retained
// recovery stays fenced until a real semantic verifier can supply proof.
func (a *adapter) recordSDATransitApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, want sdaTransitIntent, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, proof sdaTransitConvergenceProof, now time.Time) error {
	want = freezeSDATransitIntent(want)
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || !configurationOperationID.MatchString(operationID) || authorize == nil || proof == nil || now.IsZero() || !a.configurationGroupAdmissionReady() {
		return errConfigurationDeviceAdmission
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		return err
	}
	if err := declaredSDATransit(config, source, want); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if op.ID != operationID || op.Phase != "Verifying" {
		return errConfigurationStale
	}
	collect := func(ctx context.Context) (sdaTransitConvergenceObservation, []contract.GroupTargetIdentity, time.Time, error) {
		var zero sdaTransitConvergenceObservation
		devices, err := a.collectSDATransitDeviceEvidence(ctx, config, source, want, op, members, authorize, now)
		if err != nil {
			return zero, nil, time.Time{}, err
		}
		native, targets, err := a.client.sdaTransitTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return zero, nil, time.Time{}, err
		}
		if _, err := sdaTransitApplicationKey(want, native, op); err != nil {
			return zero, nil, time.Time{}, err
		}
		completed, err := a.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return zero, nil, time.Time{}, err
		}
		return sdaTransitConvergenceObservation{native: native, devices: devices, completed: completed}, targets, completed, nil
	}
	verify := func() (sdaTransitConvergenceObservation, error) {
		// The shared bracket supplies detached operation/target copies. Evidence and
		// wanted references are also copied so the proof cannot rewrite authority or
		// the observations compared after its call.
		var devices map[string]sdaTransitDeviceEvidence
		capture := func(ctx context.Context) (sdaTransitConvergenceObservation, []contract.GroupTargetIdentity, time.Time, error) {
			state, targets, completed, err := collect(ctx)
			if err == nil {
				devices = copySDATransitDeviceEvidence(state.devices)
			}
			return state, targets, completed, err
		}
		return observeConfigurationGroupConvergence(ctx, op, now, capture, func(ctx context.Context, current *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
			return proof(ctx, freezeSDATransitIntent(want), current, targets, completed, devices)
		})
	}
	observed, err := verify()
	if err != nil {
		return err
	}
	key, err := sdaTransitApplicationKey(want, observed.native, op)
	if err != nil {
		return err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	receipt := &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: key, Operation: *verified, VerifiedAt: &at}
	finalAuthorize := func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		fresh, err := verify()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(observed, fresh) {
			return errConfigurationStale
		}
		return nil
	}
	return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, finalAuthorize, now)
}
