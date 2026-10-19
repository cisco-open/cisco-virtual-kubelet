// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"net/netip"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Recover the old address from the immutable pre-mutation reservation, not
// current controller inventory after the edit. This returns retained baseline
// evidence only; live intent/worker/lease and destination proof remain separate.
func retainedManagementAddressBaseline(namespace string, ref configurationDeviceReference, op *cvk.NetworkControllerConfigurationOperation, reservation *ops.ControllerConfigurationReservation) (*managementAddressChange, error) {
	if namespace == "" || reservation == nil || reservation.Namespace != namespace || op == nil || op.Section != "inventory" || op.Action != "Update" || op.ParentID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil {
		return nil, errConfigurationTransition
	}
	if op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying" && op.Phase != "Verified" {
		return nil, errConfigurationTransition
	}
	if !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return nil, errConfigurationTransition
	}
	if err := contract.ValidateOperationBinding(reservation, op); err != nil {
		return nil, err
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(op.TargetSerial)
	if err != nil || serial != op.TargetSerial || reservation.Spec.PhysicalIdentity != serial || reservation.Spec.Device.Name == "" || reservation.Spec.Device.UID == "" || reservation.Spec.Node.Name == "" || reservation.Spec.Node.UID == "" || reservation.Spec.DeviceGeneration < 1 {
		return nil, errConfigurationStale
	}
	before, err := netip.ParseAddr(reservation.Spec.ManagementAddress)
	if err != nil || before.Zone() != "" || before.Is4In6() || !before.IsGlobalUnicast() {
		return nil, errConfigurationStale
	}
	after, err := netip.ParseAddr(ref.IP)
	if err != nil || after.Zone() != "" || after.Is4In6() || !after.IsGlobalUnicast() || before == after {
		return nil, errConfigurationStale
	}
	baseline := &managementAddressChange{device: Device{ID: op.TargetID, Serial: serial, ManagementIP: before.String()}, before: before.String(), after: after.String()}
	if managementAddressResource(ref, baseline, serial) != op.Resource {
		return nil, errConfigurationStale
	}
	return baseline, nil
}
