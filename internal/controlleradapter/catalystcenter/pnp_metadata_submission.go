// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

type metadataAdmission struct {
	installation contract.BootstrapFenceInstallation
	policyName   string
	authorize    contract.MetadataWorkerAuthorization
}

// Only this process's successful journal start can populate the slot. It is
// transient planning state, never part of a persisted resource identity.
type metadataClaimSlot struct {
	permit *contract.MetadataDispatchPermit
}

// ConfigureMetadataHandoff is an explicit installed-runtime hook. The caller
// must verify manager/worker/enrollment/settlement compatibility and actual
// Pod-bound authority. The explicit installed-runtime opt-in supplies this hook;
// default workers retain acknowledged recovery without new metadata dispatch.
func (a *adapter) ConfigureMetadataHandoff(installation contract.BootstrapFenceInstallation, metadataPolicy string, authorize contract.MetadataWorkerAuthorization) error {
	if a == nil || a.workerPodName == "" || a.workerPodUID == "" || installation.LeaseNamespace == "" || installation.PolicyName == "" || installation.ManagerUsername == "" || metadataPolicy == "" || authorize == nil || a.metadataAdmission != nil {
		return errors.New("metadata runtime is incomplete or already configured")
	}
	a.metadataAdmission = &metadataAdmission{installation: installation, policyName: metadataPolicy, authorize: authorize}
	return nil
}
func (a *adapter) metadataAdmissionReady() bool {
	return a != nil && a.metadataAdmission != nil && a.metadataAdmission.authorize != nil && a.workerPodName != "" && a.workerPodUID != ""
}
func (a *adapter) claimMetadataOperation(ctx context.Context, nc *cvk.NetworkController) (*contract.MetadataDispatchPermit, error) {
	if !a.metadataAdmissionReady() {
		return nil, errConfigurationBusy
	}
	gate := a.metadataAdmission
	return contract.ClaimMetadataOnce(ctx, a.statusReader, a.statusWriter, nc, ops.ConfigurationReservationReference{Name: a.workerPodName, UID: a.workerPodUID}, gate.installation, gate.policyName, gate.authorize, time.Now())
}
func (a *adapter) readMetadataPreparation(ctx context.Context, nc *cvk.NetworkController) error {
	if !a.metadataAdmissionReady() {
		return errConfigurationBusy
	}
	gate := a.metadataAdmission
	return contract.ReadMetadataClaimPreparation(ctx, a.statusReader, nc, ops.ConfigurationReservationReference{Name: a.workerPodName, UID: a.workerPodUID}, gate.installation, gate.policyName, gate.authorize, time.Now())
}

// Resume only the exact original Prepared metadata operation. Compilation uses
// the same creator/cohort/native guards as private new planning; it does not
// prepare a new operation, clear custody or offer Submitting/Unknown replay.
func (a *adapter) resumeOwnedPnPMetadataRemoval(ctx context.Context, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) error {
	if !a.metadataAdmissionReady() || !validPnPRemovalOperation(op) || op.Phase != "Prepared" {
		return errConfigurationBusy
	}
	op = op.DeepCopy()
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.MetadataHandoff == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || !reflect.DeepEqual(nc.Status.Configuration.MetadataHandoff.Operation, *op) {
		return errConfigurationStale
	}
	write, err := a.planPnPRemoval(ctx, c, s, op.TargetID, op)
	if err != nil {
		return err
	}
	return a.submitPreparedConfigurationReceipt(ctx, c, s, op, "inventory", write.resource, write.action, configurationCompletedWriteReceipt("inventory", write), nil, write.identity)
}
