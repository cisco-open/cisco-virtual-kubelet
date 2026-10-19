// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"time"
)

// claimConfigurationReservation uses the process's downward-API identity. The
// controller worker has no device Lease write authority. Runtime wiring must
// supply admission and workload authorization before this can replace the
// physical-write gate in submitConfigurationWrite.
func (a *adapter) claimConfigurationReservation(ctx context.Context, leaseNamespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) (*controllerconfiguration.DispatchPermit, error) {
	if a == nil || a.workerPodName == "" || a.workerPodUID == "" {
		return nil, errConfigurationDeviceAdmission
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	return controllerconfiguration.ClaimForController(ctx, a.statusReader, a.statusWriter, nc, ops.ConfigurationReservationReference{Name: a.workerPodName, UID: a.workerPodUID}, leaseNamespace, authorize, time.Now())
}

// Installed only by a runtime that has verified configuration-reservation
// admission and workload policy. Nil keeps physical execution disabled.
type configurationAdmission struct {
	protocol       string
	leaseNamespace string
	authorize      func(context.Context, *ops.ControllerConfigurationReservation) error
}

// This narrow interface allows journal sequencing tests without inventing a
// Kubernetes claim. Production supplies the non-reconstructible shared permit.
type configurationDispatchCheck interface {
	Check(context.Context, time.Time) error
}
type configurationAdmissionStep func(context.Context) (configurationDispatchCheck, error)

func (a *adapter) configurationAdmissionReady() bool {
	gate := a.configurationAdmission
	return gate != nil && gate.leaseNamespace != "" && gate.authorize != nil && a.workerPodName != "" && a.workerPodUID != ""
}

func (a *adapter) configurationGroupAdmissionReady() bool {
	return a.configurationAdmissionReady() && a.configurationAdmission.protocol == controllerconfiguration.GroupReservationProtocol
}

func (a *adapter) physicalConfigurationAdmission(write *configurationWrite) (configurationAdmissionStep, error) {
	if write == nil || write.identity.serial == "" || write.submit == nil || write.submitCompleted != nil || write.submitCreated != nil || !a.configurationAdmissionReady() {
		return nil, errConfigurationDeviceAdmission
	}
	gate := a.configurationAdmission
	return func(ctx context.Context) (configurationDispatchCheck, error) {
		return a.claimConfigurationReservation(ctx, gate.leaseNamespace, gate.authorize)
	}, nil
}

func (a *adapter) resumePhysicalConfigurationWrite(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, section string, write *configurationWrite) error {
	admit, err := a.physicalConfigurationAdmission(write)
	if err != nil {
		return err
	}
	return a.submitPreparedConfigurationReceipt(ctx, config, source, op, section, write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
		task, err := write.submit(ctx)
		return configurationReceipt{task: task}, err
	}, admit, write.identity)
}

func (a *adapter) ConfigureConfigurationReservations(leaseNamespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	return a.configureConfigurationReservations(controllerconfiguration.Protocol, leaseNamespace, authorize)
}

func (a *adapter) ConfigureConfigurationGroupReservations(leaseNamespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	return a.configureConfigurationReservations(controllerconfiguration.GroupReservationProtocol, leaseNamespace, authorize)
}

func (a *adapter) configureConfigurationReservations(protocol, leaseNamespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	if a == nil || a.workerPodName == "" || a.workerPodUID == "" || leaseNamespace == "" || authorize == nil || a.configurationAdmission != nil {
		return errors.New("configuration reservation runtime is incomplete or already configured")
	}
	a.configurationAdmission = &configurationAdmission{protocol: protocol, leaseNamespace: leaseNamespace, authorize: authorize}
	return nil
}
