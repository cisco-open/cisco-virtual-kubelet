// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/google/uuid"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"time"
)

// ReadGuardedGrant corroborates the device-worker grant with the actual Lease,
// manager acknowledgement and Node scheduling guard. It neither claims work nor
// proves workload evacuation; callers must additionally check current intent,
// worker identity, workload policy and deployed admission before dispatch.
func ReadGuardedGrant(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, leaseNamespace string, now time.Time) error {
	if r == nil || r.Spec.Protocol != Protocol {
		return errors.New("unsupported configuration grant protocol")
	}
	name, err := ReservationName(r.Spec.Controller.UID, r.Spec.OperationID)
	if err != nil || name != r.Name {
		return errors.New("invalid configuration grant identity")
	}
	return readGuardedGrantEvidence(ctx, reader, r, leaseNamespace, now)
}

// Both reservation formats use the existing per-device maintenance protocol.
// Group callers must first validate the complete group reservation binding.
func readGuardedGrantEvidence(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, leaseNamespace string, now time.Time) error {
	if err := ValidateGrantFreshness(r, now); err != nil {
		return err
	}
	reject := errors.New("configuration grant lacks its exact manager-acknowledged scheduling guard")
	if reader == nil || r == nil || r.Status.Grant == nil || leaseNamespace == "" {
		return reject
	}
	var device cvk.CiscoDevice
	var node core.Node
	var lease coordination.Lease
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &device); err != nil {
		return err
	}
	if err := reader.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &node); err != nil {
		return err
	}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: leaseNamespace, Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
		return err
	}
	if err := validateLeaseGrantEvidence(r, &lease, leaseNamespace, now); err != nil {
		return err
	}
	if string(device.UID) != r.Spec.Device.UID || device.Generation != r.Spec.DeviceGeneration || device.DeletionTimestamp != nil || string(node.UID) != r.Spec.Node.UID || node.DeletionTimestamp != nil {
		return reject
	}
	s := device.Status.MaintenanceSession
	if s == nil || (s.Phase != cvk.DeviceMaintenanceSessionAcknowledged && s.Phase != cvk.DeviceMaintenanceSessionActive) || s.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || s.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation || s.Operation.Namespace != r.Namespace || s.Operation.Name != r.Name || s.Operation.UID != string(r.UID) || s.DeviceUID != r.Spec.Device.UID || s.NodeName != node.Name || s.NodeUID != string(node.UID) || s.ControlRevision != r.Spec.ConfigGeneration || s.Lease.Namespace != leaseNamespace || s.Lease.Name != lease.Name || s.Lease.UID != string(lease.UID) || s.Lease.Holder != r.Status.Grant.Holder || s.RequestedAt.IsZero() || s.AcknowledgedAt == nil || s.AcknowledgedAt.IsZero() || s.AcknowledgedAt.Before(&s.RequestedAt) || s.AcknowledgedAt.After(now) || r.Status.Grant.GrantedAt.Before(s.AcknowledgedAt) {
		return reject
	}
	token, err := uuid.Parse(s.SessionToken)
	if err != nil || token.Version() != 4 || token.String() != s.SessionToken {
		return reject
	}
	required := map[string]string{
		managedprotocol.AnnotationMaintenanceRequestVersion:  Protocol,
		managedprotocol.AnnotationMaintenancePurpose:         managedprotocol.MaintenancePurposeConfigurationMutation,
		managedprotocol.AnnotationMaintenanceSessionToken:    s.SessionToken,
		managedprotocol.AnnotationMaintenanceOperationNS:     r.Namespace,
		managedprotocol.AnnotationMaintenanceOperationName:   r.Name,
		managedprotocol.AnnotationMaintenanceOperationUID:    string(r.UID),
		managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(r.Spec.ConfigGeneration, 10),
	}
	for k, v := range required {
		if lease.Annotations[k] != v {
			return reject
		}
	}
	requested, err := time.Parse(time.RFC3339Nano, lease.Annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
	if err != nil || !requested.Equal(s.RequestedAt.Time) {
		return reject
	}
	for _, taint := range node.Spec.Taints {
		if taint.Key == managedprotocol.MaintenanceTaintKey && taint.Value == managedprotocol.MaintenanceTaintValue && taint.Effect == core.TaintEffectNoSchedule {
			return nil
		}
	}
	return reject
}
