// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Terminal cleanup is driven by retained journal/receipt evidence, not by
// the current manifest. It may run after source removal or controller pause,
// but cannot submit a native request, replan intent or claim new ownership.
// The caller must already have matched the adapter's controller namespace.
func (r *configReconciler) recoverTerminalConfiguration(ctx context.Context, configName string) (bool, error) {
	nc, err := r.a.configurationRecoveryController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return false, nil
	}
	op := nc.Status.Configuration.Operation
	if op.ConfigName != configName {
		return false, nil
	}
	// PnP terminal recovery consumes only the original creator-bound absence
	// receipt. Custody must already have been retired by the manager. It never
	// loads current intent, creates a receipt or authorizes a native request.
	if pnpRemovalSelected(op.Resource) {
		if op.Phase != "Verified" {
			return false, nil
		}
		if nc.Status.Configuration.MetadataHandoff != nil || !pnpRemovedReceiptPresent(nc.Status.Configuration, op) {
			return true, errConfigurationBusy
		}
		return true, r.a.finishConfigurationOperation(ctx, op.ID, op.DeepCopy())
	}
	if op.PhysicalGroup == nil || (op.Phase != "Cancelling" && op.Phase != "Verified") {
		return false, nil
	}
	if !r.a.configurationAdmissionReady() {
		return true, errConfigurationDeviceAdmission
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if op.Phase == "Cancelling" {
		return true, r.a.finishConfiguration(recoveryCtx, op.ID)
	}
	return true, r.a.completeConfigurationGroup(recoveryCtx, nc)
}

func (a *adapter) authorizeConfigurationGroupMember(ctx context.Context, member *ops.ControllerConfigurationReservation) error {
	if !a.configurationAdmissionReady() || member == nil || a.workerPodName == "" || a.workerPodUID == "" || member.Spec.ControllerWorkerPod.Name != a.workerPodName || member.Spec.ControllerWorkerPod.UID != a.workerPodUID {
		return errConfigurationDeviceAdmission
	}
	return a.configurationAdmission.authorize(ctx, member)
}

// Journal events wake recovery even when the original config was deleted
// before worker restart. Existing controller list/watch permission is sufficient.
func (a *adapter) configsForTerminalRecovery(_ context.Context, obj ctrlclient.Object) []ctrl.Request {
	current, ok := obj.(*cvk.NetworkController)
	if !ok || current == nil || current.Namespace != a.key.Namespace || current.Name != a.key.Name || current.UID != a.uid || current.Spec.Type != TypeName || current.Status.Configuration == nil {
		return nil
	}
	op := current.Status.Configuration.Operation
	if op == nil || op.ConfigName == "" {
		return nil
	}
	group := op.PhysicalGroup != nil && (op.Phase == "Cancelling" || op.Phase == "Verified")
	pnp := pnpRemovalSelected(op.Resource) && op.Phase == "Verified"
	if !group && !pnp {
		return nil
	}
	return []ctrl.Request{{NamespacedName: ctrlclient.ObjectKey{Namespace: a.key.Namespace, Name: op.ConfigName}}}
}
