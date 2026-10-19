// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// No session is not by itself evidence of cleanup. The durable cancellation
// acknowledgement must already be validated by the caller. This only proves
// absence of a maintenance guard and canonical lease holder for an ungranted
// member. It does not remove guards or manufacture a settled device session.
func readUnstartedGroupCancellation(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, d *cvk.CiscoDevice, namespace string) error {
	reject := errors.New("unstarted group cancellation lacks stable device, node and idle lease evidence")
	if r == nil || d == nil || reader == nil || namespace == "" || r.Spec.Protocol != GroupReservationProtocol || r.Status.GroupCancellation == nil || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.Completion != nil || d.Status.MaintenanceSession != nil || d.DeletionTimestamp != nil || d.UID == "" || string(d.UID) != r.Spec.Device.UID || d.Namespace != r.Namespace || d.Name != r.Spec.Device.Name {
		return reject
	}
	identity := d.Status.NodeIdentity
	if identity == nil || identity.DeviceUID != r.Spec.Device.UID || identity.NodeUID != r.Spec.Node.UID || identity.NodeName != r.Spec.Node.Name || identity.PhysicalIdentity != r.Spec.PhysicalIdentity {
		return reject
	}
	canonical := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	read := func() (*core.Node, *coordination.Lease, error) {
		var node core.Node
		if err := reader.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &node); err != nil {
			return nil, nil, err
		}
		if node.UID == "" || string(node.UID) != r.Spec.Node.UID || node.DeletionTimestamp != nil {
			return nil, nil, reject
		}
		for key, value := range map[string]string{
			managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: r.Namespace,
			managedprotocol.AnnotationDeviceName: r.Spec.Device.Name, managedprotocol.AnnotationDeviceUID: r.Spec.Device.UID,
			managedprotocol.AnnotationNodeUID: r.Spec.Node.UID,
		} {
			if node.Annotations[key] != value {
				return nil, nil, reject
			}
		}
		for _, taint := range node.Spec.Taints {
			if taint.Key == managedprotocol.MaintenanceTaintKey {
				return nil, nil, reject
			}
		}
		var lease coordination.Lease
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: canonical}, &lease); err != nil {
			if apierrors.IsNotFound(err) {
				return &node, nil, nil
			}
			return nil, nil, err
		}
		if lease.UID == "" || lease.DeletionTimestamp != nil || (lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "") {
			return nil, nil, reject
		}
		return &node, &lease, nil
	}
	node, lease, err := read()
	if err != nil {
		return err
	}
	freshNode, freshLease, err := read()
	if err != nil {
		return err
	}
	if node.UID != freshNode.UID || node.ResourceVersion != freshNode.ResourceVersion || (lease == nil) != (freshLease == nil) {
		return reject
	}
	if lease != nil && (lease.UID != freshLease.UID || lease.ResourceVersion != freshLease.ResourceVersion) {
		return reject
	}
	return nil
}
