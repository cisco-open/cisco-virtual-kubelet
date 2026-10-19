// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"context"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/configurationreservation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Recovery requires the dedicated managed IOS-XE device worker identity. It is
// independent of optional gNOI upgrade/action gates and native API availability.
func (opts configReconcilerOptions) configurationReservationRecoveryEnabled(deviceName, podUID string) bool {
	m := opts.Maintenance
	return opts.ManagedTopology && !opts.ReadOnly && supportsIOSXEMutationControllers(opts.Spec) &&
		m != nil && m.ManagedTopology && m.WorkerMode == managedprotocol.WorkerModeNetworkManagement &&
		opts.DeviceNamespace != "" && deviceName != "" && opts.DeviceUID != "" &&
		opts.WorkerPodName != "" && podUID != "" &&
		m.Namespace == opts.DeviceNamespace && m.DeviceName == deviceName && m.DeviceUID == opts.DeviceUID &&
		m.WorkerPodName == opts.WorkerPodName && m.WorkerPodUID == podUID
}

func configureConfigurationReservationPreparation(r *configurationreservation.Reconciler, cfg *rest.Config, opts configReconcilerOptions, raw string) error {
	if r == nil {
		return fmt.Errorf("configuration reservation reconciler is absent")
	}
	binding, err := contract.ParseRuntimeAdmissionBinding(raw)
	if err != nil {
		return err
	}
	if binding == nil || opts.DrainDevicePodLister == nil {
		return nil
	}
	if !opts.configurationReservationRecoveryEnabled(r.DeviceName, r.PodUID) || r.Leaser == nil || cfg == nil {
		return fmt.Errorf("configuration preparation requires managed device runtime")
	}
	expectedLeaseNamespace := binding.LeaseNamespace
	if expectedLeaseNamespace == "" {
		expectedLeaseNamespace = opts.DeviceNamespace
	}
	if expectedLeaseNamespace != r.Leaser.Namespace {
		return fmt.Errorf("configuration preparation Lease namespace mismatch")
	}
	api, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	r.ControllerType = "catalyst-center"
	r.Authorize = configurationReservationWorkerAuthorizer(api, r.Reader, *binding, r.Leaser.Namespace)
	r.Prepare = func(ctx context.Context, reservation *ops.ControllerConfigurationReservation) error {
		if reservation != nil && reservation.Spec.Protocol == contract.GroupReservationProtocol {
			return opts.Maintenance.PrepareGroupConfigurationMaintenance(ctx, reservation, r.Authorize)
		}
		return opts.Maintenance.PrepareConfigurationMaintenance(ctx, reservation)
	}
	r.StrictDevicePodLister = opts.DrainDevicePodLister
	return nil
}
