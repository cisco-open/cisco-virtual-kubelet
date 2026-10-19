// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// Called only after the shared maintenance validator has checked canonical
// Lease metadata, active timing, Node/worker bindings and request envelope.
func (r *CiscoDeviceReconciler) validateConfigurationMaintenanceRequest(ctx context.Context, device *cvk.CiscoDevice, node *core.Node, lease *coordination.Lease, holder, token string, requestedAt time.Time, revision int64, operation cvk.DeviceMaintenanceObjectReference) (*cvk.DeviceMaintenanceSessionStatus, error) {
	return r.validateConfigurationMaintenanceRequestForProtocol(ctx, device, node, lease, holder, token, requestedAt, revision, operation, "", nil)
}

// Group acknowledgement and claim retention require explicit protocol authority.
// Installed group admission remains a separate runtime gate.
func (r *CiscoDeviceReconciler) validateGroupConfigurationMaintenanceRequest(ctx context.Context, device *cvk.CiscoDevice, node *core.Node, lease *coordination.Lease, holder, token string, requestedAt time.Time, revision int64, operation cvk.DeviceMaintenanceObjectReference, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) (*cvk.DeviceMaintenanceSessionStatus, error) {
	return r.validateConfigurationMaintenanceRequestForProtocol(ctx, device, node, lease, holder, token, requestedAt, revision, operation, contract.GroupReservationProtocol, authorize)
}

func (r *CiscoDeviceReconciler) validateConfigurationMaintenanceRequestForProtocol(ctx context.Context, device *cvk.CiscoDevice, node *core.Node, lease *coordination.Lease, holder, token string, requestedAt time.Time, revision int64, operation cvk.DeviceMaintenanceObjectReference, protocol string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) (*cvk.DeviceMaintenanceSessionStatus, error) {
	reject := fmt.Errorf("configuration maintenance request does not bind an authorized reservation")
	parsed, err := uuid.Parse(token)
	if err != nil || parsed.Version() != 4 || parsed.String() != token || requestedAt.After(r.now()) || lease.Annotations[managedprotocol.AnnotationMaintenancePurpose] != managedprotocol.MaintenancePurposeConfigurationMutation {
		return nil, reject
	}
	var reservation ops.ControllerConfigurationReservation
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: operation.Namespace, Name: operation.Name}, &reservation); err != nil {
		return nil, err
	}
	s := reservation.Spec
	if protocol == "" {
		protocol = contract.Protocol
		if s.Protocol == contract.GroupReservationProtocol {
			if r.APIReader == nil {
				return nil, reject
			}
			protocol, authorize = contract.GroupReservationProtocol, r.AuthorizeConfigurationReservation
		}
	}
	expectedHolder, err := contract.Holder(device.Namespace, &reservation)
	if err != nil || expectedHolder != holder || string(reservation.UID) != operation.UID || s.Protocol != protocol || s.Device.Name != device.Name || s.Device.UID != string(device.UID) || s.DeviceGeneration != device.Generation || s.Node.Name != node.Name || s.Node.UID != string(node.UID) || revision != s.ConfigGeneration || revision < 1 || s.DeviceWorkerUsername != managedNetworkWorkerUsername(node) || s.DeviceWorkerPod.Name != node.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] || s.DeviceWorkerPod.UID != node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] {
		return nil, reject
	}
	sessionMatches := func() bool {
		old := device.Status.MaintenanceSession
		if old == nil || (old.Phase != cvk.DeviceMaintenanceSessionAcknowledged && old.Phase != cvk.DeviceMaintenanceSessionActive) || old.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || old.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation || old.SessionToken != token || old.Operation != operation || old.ControlRevision != revision || old.AcknowledgedAt == nil || old.DeviceUID != string(device.UID) || old.NodeName != node.Name || old.NodeUID != string(node.UID) || old.Lease.Namespace != lease.Namespace || old.Lease.Name != lease.Name || old.Lease.UID != string(lease.UID) || old.Lease.Holder != holder || !old.RequestedAt.Time.Equal(requestedAt) {
			return false
		}
		return true
	}
	phase := cvk.DeviceMaintenanceSessionAcknowledged
	if protocol == contract.GroupReservationProtocol && reservation.Status.Claim == nil {
		if authorize == nil {
			return nil, reject
		}
		validate := func() error {
			if reservation.Status.Grant != nil {
				return contract.ReadGroupHeldMember(ctx, r.reader(), &reservation, lease.Namespace, r.now())
			}
			return contract.ReadGroupMemberPreparation(ctx, r.reader(), &reservation, r.now())
		}
		if err := validate(); err != nil {
			return nil, err
		}
		if err := authorize(ctx, reservation.DeepCopy()); err != nil {
			return nil, err
		}
		if err := validate(); err != nil {
			return nil, err
		}
		if grant := reservation.Status.Grant; grant != nil {
			if !sessionMatches() {
				return nil, reject
			}
			old := device.Status.MaintenanceSession
			if old.Phase != cvk.DeviceMaintenanceSessionAcknowledged || old.AcknowledgedAt.IsZero() || old.AcknowledgedAt.Before(&old.RequestedAt) || old.AcknowledgedAt.After(r.now()) || grant.GrantedAt.Before(old.AcknowledgedAt) {
				return nil, reject
			}
			return old.DeepCopy(), nil
		}
	}
	if reservation.Status.Claim != nil {
		// Submitted work keeps its existing guard even if desired source or worker
		// readiness changes. It cannot manufacture a first acknowledgement after
		// dispatch, or reuse another session's identity.
		old := device.Status.MaintenanceSession
		if !sessionMatches() || reservation.Status.Claim.OperationID != s.OperationID || reservation.Status.Claim.OperationDigest != s.OperationDigest {
			return nil, reject
		}
		if old.AcknowledgedAt.IsZero() || old.AcknowledgedAt.Before(&old.RequestedAt) || reservation.Status.Claim.ClaimedAt.IsZero() || reservation.Status.Claim.ClaimedAt.Before(old.AcknowledgedAt) || reservation.Status.Claim.ClaimedAt.After(r.now()) {
			return nil, reject
		}
		if protocol == contract.GroupReservationProtocol {
			if err := contract.ReadGroupClaimRetention(ctx, r.reader(), &reservation, lease, lease.Namespace, r.now()); err != nil {
				return nil, err
			}
			retained := old.DeepCopy()
			retained.Phase = cvk.DeviceMaintenanceSessionActive
			return retained, nil
		}
		if err := contract.ValidateLiveGrant(&reservation, lease, lease.Namespace, r.now()); err != nil {
			return nil, err
		}
		phase = cvk.DeviceMaintenanceSessionActive
	} else if protocol == contract.Protocol {
		if reservation.DeletionTimestamp != nil || reservation.Status.PreparationCancelledAt != nil || reservation.Status.Completion != nil || (reservation.Status.Grant != nil && reservation.Status.Grant.Phase != "Held") {
			return nil, reject
		}
		if err := contract.ReadDeviceWorkerBinding(ctx, r.reader(), &reservation, r.now()); err != nil {
			return nil, err
		}
		var controller cvk.NetworkController
		if err := r.reader().Get(ctx, client.ObjectKey{Namespace: reservation.Namespace, Name: s.Controller.Name}, &controller); err != nil {
			return nil, err
		}
		if err := contract.ReadIntentBinding(ctx, r.reader(), &reservation, controller.Spec.Type); err != nil {
			return nil, err
		}
		if err := contract.ReadControllerWorkerBinding(ctx, r.reader(), &reservation, r.now()); err != nil {
			return nil, err
		}
	}
	now := metav1.NewTime(r.now())
	return &cvk.DeviceMaintenanceSessionStatus{Phase: phase, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: token, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID)}, Holder: holder}, Operation: operation, DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID), RequestedAt: metav1.NewTime(requestedAt), AcknowledgedAt: &now, ControlRevision: revision, Message: "manager acknowledged the exact controller-configuration maintenance request"}, nil
}

// Settlement requires the same retained Lease incarnation to be idle. Lease
// expiry, a missing reservation, or a completion flag alone cannot unfence a Node.
func (r *CiscoDeviceReconciler) settleConfigurationMaintenance(ctx context.Context, device *cvk.CiscoDevice, node *core.Node, lease *coordination.Lease, decision managedMaintenanceDecision) managedMaintenanceDecision {
	return r.settleConfigurationMaintenanceForProtocol(ctx, device, node, lease, decision, "", nil)
}

// Group settlement decision; the caller still owns admitted manager
// status/Node writes and retained settlement publication.
func (r *CiscoDeviceReconciler) settleGroupConfigurationMaintenance(ctx context.Context, device *cvk.CiscoDevice, node *core.Node, lease *coordination.Lease, decision managedMaintenanceDecision, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) managedMaintenanceDecision {
	return r.settleConfigurationMaintenanceForProtocol(ctx, device, node, lease, decision, contract.GroupReservationProtocol, authorize)
}

func (r *CiscoDeviceReconciler) settleConfigurationMaintenanceForProtocol(ctx context.Context, device *cvk.CiscoDevice, node *core.Node, lease *coordination.Lease, decision managedMaintenanceDecision, protocol string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) managedMaintenanceDecision {
	session := decision.session
	reject := func(err error) managedMaintenanceDecision {
		decision.err = err
		decision.message = truncateTopologyMessage(err.Error())
		return decision
	}
	namespace := r.LeaseNamespace
	if namespace == "" {
		namespace = device.Namespace
	}
	canonical := engine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily)
	if session.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation || lease == nil || lease.DeletionTimestamp != nil || lease.UID == "" || lease.Namespace != namespace || lease.Name != canonical || session.Lease.Namespace != namespace || session.Lease.Name != canonical || session.Lease.UID != string(lease.UID) || (lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "") {
		return reject(fmt.Errorf("configuration maintenance settlement requires its exact idle canonical Lease"))
	}
	var reservation ops.ControllerConfigurationReservation
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: session.Operation.Namespace, Name: session.Operation.Name}, &reservation); err != nil {
		return reject(fmt.Errorf("read configuration maintenance reservation: %w", err))
	}
	s, g := reservation.Spec, reservation.Status.Grant
	if protocol == "" {
		protocol = contract.Protocol
		if s.Protocol == contract.GroupReservationProtocol {
			if r.APIReader == nil {
				return reject(fmt.Errorf("group settlement requires uncached reads"))
			}
			protocol, authorize = contract.GroupReservationProtocol, r.AuthorizeConfigurationReservation
		}
	}
	holder, err := contract.Holder(device.Namespace, &reservation)
	if err != nil || string(reservation.UID) != session.Operation.UID || s.Protocol != protocol || s.Device.Name != device.Name || s.Device.UID != string(device.UID) || s.Node.Name != node.Name || s.Node.UID != string(node.UID) || s.ConfigGeneration != session.ControlRevision || session.Lease.Holder != holder {
		return reject(fmt.Errorf("configuration maintenance reservation has not released this session"))
	}
	cancelledGroup := protocol == contract.GroupReservationProtocol && reservation.Status.GroupCancellation != nil
	if protocol == contract.GroupReservationProtocol {
		if authorize == nil || (!cancelledGroup && reservation.Status.Claim == nil) {
			return reject(fmt.Errorf("group completion settlement requires claimed work and manager authority"))
		}
		validate := contract.ReadGroupMemberCompletion
		if cancelledGroup {
			validate = contract.ReadGroupMemberCancellation
		}
		if err := validate(ctx, r.reader(), &reservation, r.now()); err != nil {
			return reject(err)
		}
		if err := authorize(ctx, reservation.DeepCopy()); err != nil {
			return reject(err)
		}
		if err := validate(ctx, r.reader(), &reservation, r.now()); err != nil {
			return reject(err)
		}
		var retainedLease coordination.Lease
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(lease), &retainedLease); err != nil {
			return reject(err)
		}
		if retainedLease.UID != lease.UID || retainedLease.ResourceVersion != lease.ResourceVersion || retainedLease.DeletionTimestamp != nil || (retainedLease.Spec.HolderIdentity != nil && *retainedLease.Spec.HolderIdentity != "") {
			return reject(fmt.Errorf("group maintenance lease changed during settlement authorization"))
		}
	}
	if cancelledGroup {
		if session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() || session.AcknowledgedAt.Before(&session.RequestedAt) || reservation.Status.GroupCancellation.AcknowledgedAt.Before(session.AcknowledgedAt) {
			return reject(fmt.Errorf("group cancellation predates maintenance acknowledgement"))
		}
		if g != nil && (g.Phase != "Released" || g.Holder != holder || g.LeaseNamespace != namespace || g.Lease.Name != canonical || g.Lease.UID != string(lease.UID)) {
			return reject(fmt.Errorf("cancelled group grant has not released its session"))
		}
	} else if reservation.Status.PreparationCancelledAt != nil {
		if err := contract.ValidatePreparationCancellation(&reservation, r.now()); err != nil {
			return reject(err)
		}
		if session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() || reservation.Status.PreparationCancelledAt.Before(session.AcknowledgedAt) {
			return reject(fmt.Errorf("configuration cancellation predates maintenance acknowledgement"))
		}
	} else if g == nil || g.Phase != "Released" || g.Holder != holder || g.LeaseNamespace != namespace || g.Lease.Name != canonical || g.Lease.UID != string(lease.UID) {
		return reject(fmt.Errorf("configuration maintenance reservation has not released this session"))
	}
	if reservation.Status.Claim != nil {
		if session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() || session.AcknowledgedAt.Before(&session.RequestedAt) || reservation.Status.Claim.ClaimedAt.Before(session.AcknowledgedAt) {
			return reject(fmt.Errorf("configuration claim predates maintenance acknowledgement"))
		}
		var controller cvk.NetworkController
		if err := r.reader().Get(ctx, client.ObjectKey{Namespace: reservation.Namespace, Name: s.Controller.Name}, &controller); err != nil {
			return reject(fmt.Errorf("read retained configuration receipt: %w", err))
		}
		if protocol == contract.Protocol {
			if err := contract.ValidateCompletionReceipt(&reservation, &controller, r.now()); err != nil {
				return reject(err)
			}
		}
	} else if reservation.Status.Completion != nil {
		return reject(fmt.Errorf("unclaimed configuration reservation contains completion evidence"))
	}
	decision.session.Phase = cvk.DeviceMaintenanceSessionSettled
	decision.session.Message = "manager verified configuration reservation release and settled maintenance"
	decision.guard = false
	decision.status = metav1.ConditionTrue
	decision.reason = "SessionSettled"
	decision.message = decision.session.Message
	return decision
}

// Record a completed session before a subsequent Lease request can replace it.
// Missing evidence means the manager already retired the protected reservation;
// retirement must itself wait for journal release.
func (r *CiscoDeviceReconciler) retainConfigurationSettlement(ctx context.Context, device *cvk.CiscoDevice) error {
	s := device.Status.MaintenanceSession
	if s == nil || s.Phase != cvk.DeviceMaintenanceSessionSettled || s.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 {
		return nil
	}
	if s.Operation.Namespace != device.Namespace {
		return fmt.Errorf("settled configuration session namespace is invalid")
	}
	var reservation ops.ControllerConfigurationReservation
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: s.Operation.Namespace, Name: s.Operation.Name}, &reservation); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if string(reservation.UID) != s.Operation.UID {
		return fmt.Errorf("settled configuration reservation was replaced")
	}
	if reservation.Spec.Protocol == contract.GroupReservationProtocol {
		return r.retainGroupConfigurationSettlement(ctx, device, r.AuthorizeConfigurationReservation)
	}
	return contract.PublishManagerSettlement(ctx, r.reader(), r.Client, &reservation, r.now())
}

// Group counterpart: retain settlement before another maintenance request
// can replace the session. Missing evidence fails closed until group retirement
// is connected; it does not inherit the v1 missing-reservation shortcut.
func (r *CiscoDeviceReconciler) retainGroupConfigurationSettlement(ctx context.Context, device *cvk.CiscoDevice, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	if device == nil || authorize == nil || r.APIReader == nil || r.Client == nil {
		return fmt.Errorf("group settlement retention authority unavailable")
	}
	s := device.Status.MaintenanceSession
	if s == nil || s.Phase != cvk.DeviceMaintenanceSessionSettled || s.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || s.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation || s.Operation.Namespace != device.Namespace {
		return fmt.Errorf("group settlement session unavailable")
	}
	var row ops.ControllerConfigurationReservation
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: s.Operation.Namespace, Name: s.Operation.Name}, &row); err != nil {
		return err
	}
	if string(row.UID) != s.Operation.UID || row.Spec.Device.UID != string(device.UID) || row.Spec.Device.Name != device.Name {
		return fmt.Errorf("settled group member was replaced")
	}
	namespace := r.LeaseNamespace
	if namespace == "" {
		namespace = device.Namespace
	}
	if row.Status.GroupCancellation != nil {
		if err := contract.ReadGroupMemberCancellation(ctx, r.APIReader, &row, r.now()); err != nil {
			return err
		}
		var nc cvk.NetworkController
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: row.Namespace, Name: row.Spec.Controller.Name}, &nc); err != nil {
			return err
		}
		return contract.PublishCancelledGroupManagerSettlement(ctx, r.APIReader, r.Client, &nc, &row, namespace, authorize, r.now())
	}
	return contract.RetainGroupMemberSettlement(ctx, r.APIReader, r.Client, &row, namespace, authorize, r.now())
}
