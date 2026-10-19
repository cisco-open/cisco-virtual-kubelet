// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"time"
)

// Bridge native provisioning and the shared journal. The enclosing inventory
// stage must order prerequisites and apply physical admission before submission.
type wiredProvisionExecutor struct {
	adapter *adapter
	config  *configapi.NetworkControllerConfig
	source  *configSourceSnapshot
	desired inventoryDeviceIntent
	state   string
}

func (*wiredProvisionExecutor) count() int { return 1 }
func (e *wiredProvisionExecutor) matchesRevision(nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation) bool {
	if e.config == nil || nc == nil || op == nil || !validWiredProvisionResource(op.Resource) || op.PhysicalGroup != nil || op.Section != "inventory" || op.ConfigName != e.config.Name || op.ConfigUID != string(e.config.UID) || op.ConfigGeneration != e.config.Generation || op.ControllerGeneration != nc.Generation || len(op.CredentialVersions) != 0 {
		return false
	}
	uid, version := "", ""
	if e.source != nil {
		uid = string(e.source.uid)
		version = e.source.version
	}
	return op.SourceUID == uid && op.SourceVersion == version
}
func (e *wiredProvisionExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e.adapter == nil || e.config == nil {
		return nil, errConfigurationTransition
	}
	a := e.adapter
	if err := a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return a.planWiredProvisionRevision(ctx, e.config, e.source, e.desired, e.state)
	}
	op := nc.Status.Configuration.Operation
	if op.Phase != "Prepared" || !e.matchesRevision(nc, op) {
		return nil, errConfigurationBusy
	}
	write, err := a.client.planWiredDeviceProvision(ctx, e.desired, e.state)
	if err != nil {
		return nil, err
	}
	// A now-converged POST is not authorization to discard Prepared evidence.
	if write == nil || write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || write.identity.serial != op.TargetSerial {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return write, nil
}
func (e *wiredProvisionExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e.adapter == nil || e.config == nil || op == nil || op.Phase != "Verifying" {
		return false, errConfigurationTransition
	}
	a := e.adapter
	if err := a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return false, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if !e.matchesRevision(nc, op) || nc.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) {
		return false, errConfigurationStale
	}
	verified, err := a.client.verifyWiredDeviceProvision(ctx, e.desired, e.state, op, time.Now())
	if err != nil || !verified {
		return false, err
	}
	// Authoritative readback can race a source or journal change. Completion must
	// remain bound to the operation that was actually verified.
	if err := a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return false, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion || fresh.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(fresh.Status.Configuration.Operation, op) {
		return false, errConfigurationStale
	}
	return true, nil
}

var _ configurationExecutor = (*wiredProvisionExecutor)(nil)
