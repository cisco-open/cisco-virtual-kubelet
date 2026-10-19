// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

// The native portion of a combined native/device proof. It never establishes
// IS-IS absence; the independent strict device profile must also succeed.
func lanLinkDeleteResultDigest(snapshot *lanLinkAdjacencySnapshot) (string, error) {
	if snapshot == nil || snapshot.endpoints == nil {
		return "", errConfigurationBusy
	}
	for _, endpoint := range []lanLinkEndpoint{snapshot.endpoints.primary, snapshot.endpoints.secondary} {
		if endpoint.port.IPv4 != "" || endpoint.port.Mask != "" {
			return "", errConfigurationBusy
		}
	}
	if link := snapshot.link; link != nil && (link.StartIP != "" || link.EndIP != "" || link.StartMask != "" || link.EndMask != "") {
		return "", errConfigurationBusy
	}
	physical, err := lanLinkDeletePhysicalDigest(snapshot.endpoints)
	if err != nil {
		return "", err
	}
	return lanLinkDigest([]string{physical, "ipv4-isis-deconfigured:v1"}), nil
}

func (a *adapter) lanLinkDeleteDeviceProof(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanLinkIntent, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) configurationGroupConvergenceProof {
	return a.lanLinkWorkerProof(config, source, members, authorize, now, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (*lanLinkProofState, error) {
		native, targets, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, op, now)
		if err != nil {
			return nil, err
		}
		result, err := lanLinkDeleteResultDigest(native)
		if err != nil {
			return nil, err
		}
		return &lanLinkProofState{native.endpoints, targets, result}, nil
	}, lanLinkDeleteProofCommands, verifyLANLinkDeleteCLI)
}

func (a *adapter) verifiedLANLinkDeleteReceipt(ctx context.Context, config *configapi.NetworkControllerConfig, want lanLinkIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time, proof configurationGroupConvergenceProof) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	binding, err := validateLANLinkDeleteCompletionOperation(op, now)
	if err != nil {
		return nil, err
	}
	if op.Phase != "Verifying" || proof == nil {
		return nil, errConfigurationDeviceAdmission
	}
	collect := func(ctx context.Context) (*lanLinkAdjacencySnapshot, []contract.GroupTargetIdentity, time.Time, error) {
		completed, err := a.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		if completed.IsZero() || completed.After(now.UTC().Truncate(time.Second)) {
			return nil, nil, time.Time{}, errConfigurationBusy
		}
		native, targets, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, op, now)
		return native, targets, completed, err
	}
	native, err := observeConfigurationGroupConvergence(ctx, op, now, collect, proof)
	if err != nil {
		return nil, err
	}
	result, err := lanLinkDeleteResultDigest(native)
	if err != nil {
		return nil, err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "lan_automation", Resource: "lan-link-delete:" + binding.logical + ":" + binding.declaration + ":" + result, Operation: *verified, VerifiedAt: &at}, nil
}

func (a *adapter) recordLANLinkDeleteApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, proof configurationGroupConvergenceProof, now time.Time) error {
	return a.recordLANLinkApplication(ctx, config, operationID, members, authorize, proof, now, func(ctx context.Context, source *configSourceSnapshot, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, guarded configurationGroupConvergenceProof) (*cvk.NetworkControllerConfigurationActionApplication, error) {
		want, err := a.retainedLANLinkDelete(ctx, config, source, nc, op, now)
		if err != nil {
			return nil, err
		}
		return a.verifiedLANLinkDeleteReceipt(ctx, config, want, op, now, guarded)
	})
}
