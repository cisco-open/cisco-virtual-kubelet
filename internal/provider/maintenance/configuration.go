// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package maintenance

import (
	"context"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/google/uuid"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"time"
)

// publishConfigurationMaintenanceRequest uses the existing canonical Lease
// request envelope with a distinct protocol and purpose. It is intentionally
// private to the configuration preparation path.
// This publishes no grant and never mutates a Node or controller appliance.
func (c *Coordinator) publishConfigurationMaintenanceRequest(ctx context.Context, r *ops.ControllerConfigurationReservation, node *core.Node) (publishedRequest, error) {
	return c.publishConfigurationMaintenanceRequestForProtocol(ctx, r, node, contract.Protocol)
}

func (c *Coordinator) publishConfigurationMaintenanceRequestForProtocol(ctx context.Context, r *ops.ControllerConfigurationReservation, node *core.Node, protocol string) (publishedRequest, error) {
	if c == nil || c.Client == nil || !c.ManagedTopology || c.WorkerMode != managedprotocol.WorkerModeNetworkManagement || r == nil || node == nil || r.Spec.Protocol != protocol || (protocol != contract.Protocol && protocol != contract.GroupReservationProtocol) || r.Namespace != c.Namespace || r.Spec.Device.Name != c.DeviceName || r.Spec.Device.UID != c.DeviceUID || r.Spec.Node.Name != node.Name || r.Spec.Node.UID != string(node.UID) || r.Spec.DeviceWorkerPod.Name != c.WorkerPodName || r.Spec.DeviceWorkerPod.UID != c.WorkerPodUID || r.Spec.DeviceWorkerUsername != c.ExpectedWorkerUsername || r.Spec.ConfigGeneration < 1 || r.Status.PreparationCancelledAt != nil || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.Completion != nil {
		return publishedRequest{}, fmt.Errorf("configuration maintenance binding is incomplete")
	}
	if err := c.validateManagedNode(node); err != nil {
		return publishedRequest{}, err
	}
	holder, err := contract.Holder(c.Namespace, r)
	if err != nil {
		return publishedRequest{}, err
	}
	key := client.ObjectKey{Namespace: c.LeaseNamespace, Name: engine.LeaseName(devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily)}
	var result publishedRequest
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if protocol == contract.GroupReservationProtocol {
			if err := contract.ReadGroupMemberPreparation(ctx, c.Client, r, time.Now()); err != nil {
				return err
			}
		}
		var lease coordination.Lease
		if err := c.Client.Get(ctx, key, &lease); err != nil {
			return err
		}
		if err := validateActiveManagedMutationLease(&lease, holder); err != nil {
			return err
		}
		if lease.UID == "" || lease.ResourceVersion == "" || lease.DeletionTimestamp != nil || lease.Spec.RenewTime.After(time.Now()) || !time.Now().Before(lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second)) {
			return fmt.Errorf("configuration maintenance requires a live canonical Lease")
		}
		if err := c.validateManagedLeaseBinding(&lease, node); err != nil {
			return err
		}
		before := lease.DeepCopy()
		annotations := lease.Annotations
		token := annotations[managedprotocol.AnnotationMaintenanceSessionToken]
		requestedAt, parseErr := time.Parse(time.RFC3339Nano, annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
		if hasManagedMaintenanceRequestAnnotations(annotations) {
			parsed, err := uuid.Parse(token)
			if err != nil || parsed.Version() != 4 || parsed.String() != token || parseErr != nil || requestedAt.After(time.Now()) || annotations[managedprotocol.AnnotationMaintenanceRequestVersion] != managedprotocol.ConfigurationProtocolVersion || annotations[managedprotocol.AnnotationMaintenancePurpose] != managedprotocol.MaintenancePurposeConfigurationMutation || annotations[managedprotocol.AnnotationMaintenanceOperationNS] != r.Namespace || annotations[managedprotocol.AnnotationMaintenanceOperationName] != r.Name || annotations[managedprotocol.AnnotationMaintenanceOperationUID] != string(r.UID) || annotations[managedprotocol.AnnotationMaintenanceControlRevision] != strconv.FormatInt(r.Spec.ConfigGeneration, 10) {
				return fmt.Errorf("canonical Lease retains another or malformed maintenance request")
			}
		} else {
			token = uuid.NewString()
			requestedAt = time.Now().UTC().Truncate(time.Second)
		}
		values := map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: managedprotocol.ConfigurationProtocolVersion, managedprotocol.AnnotationMaintenancePurpose: managedprotocol.MaintenancePurposeConfigurationMutation, managedprotocol.AnnotationMaintenanceSessionToken: token, managedprotocol.AnnotationMaintenanceRequestedAt: requestedAt.Format(time.RFC3339Nano), managedprotocol.AnnotationMaintenanceOperationNS: r.Namespace, managedprotocol.AnnotationMaintenanceOperationName: r.Name, managedprotocol.AnnotationMaintenanceOperationUID: string(r.UID), managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(r.Spec.ConfigGeneration, 10)}
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		for k, v := range values {
			lease.Annotations[k] = v
		}
		if !stringMapEqual(before.Annotations, lease.Annotations) {
			if err := c.Client.Patch(ctx, &lease, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
		}
		result = publishedRequest{Token: token, RequestedAt: requestedAt, ControlRevision: r.Spec.ConfigGeneration, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, LeaseNamespace: lease.Namespace, LeaseName: lease.Name, LeaseUID: string(lease.UID), Holder: holder, Operation: cvk.DeviceMaintenanceObjectReference{Namespace: r.Namespace, Name: r.Name, UID: string(r.UID)}}
		return nil
	})
	return result, err
}

// PrepareConfigurationMaintenance publishes and verifies the manager-owned
// scheduling guard for an unclaimed reservation. Call only after acquiring its
// canonical Lease. This does not prove workload evacuation, authorize dispatch,
// or publish a grant; the caller must perform those independent checks.
func (c *Coordinator) PrepareConfigurationMaintenance(ctx context.Context, expected *ops.ControllerConfigurationReservation) error {
	return c.prepareConfigurationMaintenance(ctx, expected, contract.Protocol, nil)
}

// PrepareGroupConfigurationMaintenance reuses the per-device maintenance
// envelope after complete group validation. Authorize must verify installed
// group admission/worker compatibility; this method does not enable runtime
// group reconciliation or prove native workload evacuation.
func (c *Coordinator) PrepareGroupConfigurationMaintenance(ctx context.Context, expected *ops.ControllerConfigurationReservation, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	if authorize == nil {
		return fmt.Errorf("group maintenance requires explicit admission authorization")
	}
	return c.prepareConfigurationMaintenance(ctx, expected, contract.GroupReservationProtocol, authorize)
}

func (c *Coordinator) prepareConfigurationMaintenance(ctx context.Context, expected *ops.ControllerConfigurationReservation, protocol string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	if c == nil || c.Client == nil || !c.ManagedTopology || c.WorkerMode != managedprotocol.WorkerModeNetworkManagement || c.Namespace == "" || c.DeviceName == "" || c.DeviceUID == "" || c.NodeName == "" || c.LeaseNamespace == "" || expected == nil || expected.UID == "" || expected.ResourceVersion == "" {
		return fmt.Errorf("configuration maintenance coordinator or reservation is incomplete")
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	currentReservation := func() (*ops.ControllerConfigurationReservation, error) {
		var current ops.ControllerConfigurationReservation
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(expected), &current); err != nil {
			return nil, err
		}
		if current.Spec.Protocol != protocol || current.UID != expected.UID || current.ResourceVersion != expected.ResourceVersion || current.DeletionTimestamp != nil || !reflect.DeepEqual(current.Spec, expected.Spec) || current.Status.PreparationCancelledAt != nil || current.Status.Grant != nil || current.Status.Claim != nil || current.Status.Completion != nil {
			return nil, fmt.Errorf("configuration reservation changed during maintenance preparation")
		}
		if protocol == contract.GroupReservationProtocol {
			if err := contract.ReadGroupMemberPreparation(ctx, c.Client, &current, time.Now()); err != nil {
				return nil, err
			}
			if err := authorize(ctx, current.DeepCopy()); err != nil {
				return nil, err
			}
			if err := contract.ReadGroupMemberPreparation(ctx, c.Client, &current, time.Now()); err != nil {
				return nil, err
			}
		}
		return &current, nil
	}
	reservation, err := currentReservation()
	if err != nil {
		return err
	}
	var node core.Node
	if err := c.Client.Get(ctx, client.ObjectKey{Name: c.NodeName}, &node); err != nil {
		return err
	}
	request, err := c.publishConfigurationMaintenanceRequestForProtocol(ctx, reservation, &node, protocol)
	if err != nil {
		return err
	}
	var device cvk.CiscoDevice
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: c.DeviceName}, &device); err != nil {
		return err
	}
	if string(device.UID) != c.DeviceUID || device.DeletionTimestamp != nil || device.Generation != reservation.Spec.DeviceGeneration || device.Status.NodeIdentity == nil || device.Status.NodeIdentity.DeviceUID != c.DeviceUID || device.Status.NodeIdentity.NodeName != node.Name || device.Status.NodeIdentity.NodeUID != string(node.UID) {
		return fmt.Errorf("configuration maintenance device identity changed")
	}
	if err := validateMaintenanceAcknowledgement(device.Status.MaintenanceSession, request, &node); err != nil {
		return err
	}
	// Fetch the Node again after observing acknowledgement: a stale pre-request
	// read must not masquerade as proof that the manager installed the guard.
	if err := c.Client.Get(ctx, client.ObjectKey{Name: c.NodeName}, &node); err != nil {
		return err
	}
	if err := c.validateManagedNode(&node); err != nil {
		return err
	}
	if string(node.UID) != reservation.Spec.Node.UID || !hasMaintenanceTaint(node.Spec.Taints) {
		return fmt.Errorf("configuration maintenance requires the exact guarded Node")
	}
	if _, err := currentReservation(); err != nil {
		return err
	}
	// Revalidate Lease ownership and preserve the same request identity after all
	// acknowledgement reads. A lost or replaced Lease cannot prepare a grant.
	latest, err := c.publishConfigurationMaintenanceRequestForProtocol(ctx, reservation, &node, protocol)
	if err != nil {
		return err
	}
	if latest != request {
		return fmt.Errorf("configuration maintenance request changed during acknowledgement")
	}
	return nil
}
