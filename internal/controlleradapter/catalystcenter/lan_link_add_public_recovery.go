// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"strings"
	"time"
)

// Observe retained acknowledged work before unsupported/new intent compilation.
// This path never claims a group, reconstructs a permit or submits a LAN update.
// Independent read-only device checks use existing DeviceOperation workers.
func (r *configReconciler) recoverAcknowledgedLANLink(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, error) {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return false, nil
	}
	op := nc.Status.Configuration.Operation
	if op.Section != "lan_automation" || !(strings.HasPrefix(op.Resource, lanLinkAddFamily) || strings.HasPrefix(op.Resource, lanLinkDeleteFamily)) || op.ConfigName != config.Name || op.Phase == "Prepared" {
		return false, nil
	}
	if op.Phase == "Submitting" {
		return true, errLANLinkSubmissionUncertain
	}
	if op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying" {
		return false, nil
	}
	if !r.a.configurationAdmissionReady() || !supportedLANLinkAddConfig(config) || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || source == nil || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return true, errConfigurationStale
	}
	recovery, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	members, err := contract.ReadAcknowledgedGroupReservations(recovery, r.a.statusReader, nc, time.Now())
	if err != nil {
		return true, err
	}
	authorize := func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		for _, member := range current {
			if err := r.a.authorizeConfigurationGroupMember(ctx, member); err != nil {
				return err
			}
		}
		return nil
	}
	if err := authorize(recovery, members); err != nil {
		return true, err
	}
	if strings.HasPrefix(op.Resource, lanLinkDeleteFamily) {
		if op.Phase != "Verifying" {
			return true, r.a.beginLANLinkDeleteVerification(recovery, config, source, members, authorize, time.Now())
		}
		now := time.Now()
		want, err := r.a.retainedLANLinkDelete(recovery, config, source, nc, op, now)
		if err != nil {
			return true, err
		}
		proof := r.a.lanLinkDeleteDeviceProof(config, source, want, members, authorize, now)
		return true, r.a.recordLANLinkDeleteApplication(recovery, config, op.ID, members, authorize, proof, now)
	}
	if op.Phase == "Verifying" {
		want, err := r.a.retainedLANLinkAdd(recovery, source, op, time.Now())
		if err != nil {
			return true, err
		}
		now := time.Now()
		proof := r.a.lanLinkDeviceConvergenceProof(config, source, want, members, authorize, now)
		return true, r.a.recordLANLinkAddApplication(recovery, config, op.ID, members, authorize, proof, now)
	}
	return true, r.a.beginLANLinkAddVerification(recovery, config, source, members, authorize, time.Now())
}

func (r *configReconciler) evaluateRetainedLANLink(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (*configSourceSnapshot, bool, string, string) {
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "lan_automation", State: configapi.NetworkControllerSectionStateApplying}}
	source, err := loadControllerSource(ctx, r.a.statusReader, config)
	if err != nil {
		return nil, true, "SourceUnavailable", "Retained LAN link operation remains fenced while its source is unavailable"
	}
	handled, err := r.recoverAcknowledgedLANLink(ctx, nc, config, source)
	if !handled {
		return source, false, "", ""
	}
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "lan_automation", State: configapi.NetworkControllerSectionStateApplying}}
	if errors.Is(err, errLANLinkSubmissionUncertain) {
		return source, true, "GroupSubmissionOutcomeUnknown", "LAN link update outcome is unacknowledged; journal and both endpoint reservations remain retained"
	}
	if errors.Is(err, errLANLinkDeviceProofPending) {
		return source, true, "GroupDeviceVerificationPending", "Read-only device-worker checks must verify both LAN interfaces against the admitted ADD or DELETE result before completion; endpoint claims remain retained"
	}

	if errors.Is(err, errLANLinkLegacyRecovery) {
		return source, true, "LegacyGroupRecoveryUnavailable", "Legacy LAN link identity cannot prove the original new-link baseline; automatic completion and replay are refused"
	}
	if err != nil {
		return source, true, "GroupVerificationPending", "Retained LAN link task awaits unchanged source, complete actor/endpoint identity and native evidence"
	}
	return source, true, "ApplyProgressing", "Retained LAN link operation advanced to verification without another native request"
}
