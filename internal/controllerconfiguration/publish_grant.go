// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// PublishGrant is called by the bound device worker after canonical mutation
// Lease acquisition and maintenance preparation. It never acquires or releases
// that Lease. Readers must be uncached; podUID must be this process's trusted
// downward-API identity, and leaseNamespace its configured Lease namespace.
// Controller/source admission and rollout readiness remain separate gates.
func PublishGrant(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, leaseNamespace, podUID string, now time.Time) error {
	return publishGrant(ctx, reader, writer, expected, leaseNamespace, podUID, now, false)
}

// PublishGroupGrant reuses lease publication after complete Prepared group and
// live member validation. It corroborates acknowledged maintenance guards and
// empty Kubernetes workloads. Installed group admission and native workload
// preparation remain mandatory caller gates.
func PublishGroupGrant(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, leaseNamespace, podUID string, now time.Time) error {
	return publishGrant(ctx, reader, writer, expected, leaseNamespace, podUID, now, true)
}

func publishGrant(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, leaseNamespace, podUID string, now time.Time, group bool) error {
	reject := errors.New("configuration grant requires current unclaimed worker evidence")
	if reader == nil || writer == nil || expected == nil || expected.ResourceVersion == "" || leaseNamespace == "" || podUID == "" || now.IsZero() {
		return reject
	}
	var r ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &r); err != nil {
		return err
	}
	if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(r.Spec, expected.Spec) || r.Spec.DeviceWorkerPod.UID != podUID || r.Status.PreparationCancelledAt != nil || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.Completion != nil {
		return errors.New("configuration grant reservation changed, was granted, or names another Pod")
	}
	readBinding := ReadDeviceWorkerBinding
	if group {
		if r.Spec.Protocol != GroupReservationProtocol {
			return reject
		}
		readBinding = readDeviceWorkerObjects
	}
	if err := readBinding(ctx, reader, &r, now); err != nil {
		return err
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if nc.UID == "" || string(nc.UID) != r.Spec.Controller.UID || nc.Generation != r.Spec.ControllerGeneration || nc.DeletionTimestamp != nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Prepared" {
		return errors.New("configuration grant has no current Prepared controller journal")
	}
	if group {
		if err := readPreparedGroupForGrant(ctx, reader, &nc, &r, now); err != nil {
			return err
		}
	} else {
		serial, serialErr := topologyidentity.CanonicalPhysicalIdentity(nc.Status.Configuration.Operation.TargetSerial)
		if serialErr != nil || serial != r.Spec.PhysicalIdentity {
			return errors.New("configuration journal physical target differs from reservation")
		}
		if err := ValidateOperationBinding(&r, nc.Status.Configuration.Operation); err != nil {
			return err
		}
	}
	name := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	var lease coordination.Lease
	if err := reader.Get(ctx, client.ObjectKey{Namespace: leaseNamespace, Name: name}, &lease); err != nil {
		return err
	}
	holder, err := Holder(r.Namespace, &r)
	if err != nil {
		return err
	}
	if lease.Spec.LeaseTransitions == nil {
		return reject
	}
	r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: leaseNamespace, Lease: ops.ConfigurationReservationReference{Name: name, UID: string(lease.UID)}, Holder: holder, LeaseTransitions: *lease.Spec.LeaseTransitions, GrantedAt: metav1.NewTime(now)}
	validateLease := ValidateLiveGrant
	if group {
		validateLease = validateLeaseGrantEvidence
	}
	if err := validateLease(&r, &lease, leaseNamespace, now); err != nil {
		return err
	}
	if group {
		if err := readGuardedGrantEvidence(ctx, reader, &r, leaseNamespace, now); err != nil {
			return err
		}
		if err := readEmptyConfigurationWorkloads(ctx, reader, &r); err != nil {
			return err
		}
	}
	// Recheck the journal revision after collecting the Lease. Dispatch still
	// revalidates source and authority; this is not a multi-object transaction.
	var fresh cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&nc), &fresh); err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return errors.New("configuration controller changed during grant reads")
	}
	if group {
		if err := readPreparedGroupForGrant(ctx, reader, &fresh, &r, now); err != nil {
			return err
		}
	}
	if err := writer.Status().Update(ctx, &r); err != nil {
		return err
	}
	return verifySettlementPersistence(ctx, reader, &r)
}
