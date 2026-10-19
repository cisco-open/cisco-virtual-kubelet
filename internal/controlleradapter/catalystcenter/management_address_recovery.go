// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"slices"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Observe the native endpoint stage of a retained operation without submitting,
// claiming, advancing the journal or releasing a lease. True is native-stage
// evidence only: the public bridge must still perform address/worker rollout
// and publish its complete verified receipt through the existing lifecycle.
func (a *adapter) observeManagementAddressRecovery(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, endpointProof managementAddressGuard) (bool, error) {
	if a == nil || a.client == nil || config == nil || source == nil || endpointProof == nil || !a.configurationAdmissionReady() || a.configurationAdmission.protocol != contract.Protocol {
		return false, errConfigurationDeviceAdmission
	}
	if config.Spec.Mode != configapi.NetworkControllerApplyModeApply || (config.Spec.Scope != "" && config.Spec.Scope != "global") || !slices.Contains(config.Spec.ManagedSections, "inventory") {
		return false, errConfigurationStale
	}
	intent, err := parseInventoryIntent(source.raw)
	if err != nil {
		return false, err
	}
	declared := 0
	for _, device := range intent.devices {
		if reflect.DeepEqual(device.reference, ref) {
			declared++
		}
	}
	if declared != 1 {
		return false, errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return false, errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if op.Phase != "Verifying" || op.Section != "inventory" || op.Action != "Update" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return false, errConfigurationStale
	}
	check := func(ctx context.Context) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		owns := 0
		for _, owner := range current.Status.Configuration.Owners {
			if owner.Section == "inventory" {
				if owner.ConfigName != config.Name || owner.ConfigUID != string(config.UID) {
					return errConfigurationOwned
				}
				owns++
			}
		}
		if owns != 1 {
			return errConfigurationOwned
		}
		reservation, err := a.configurationReservation(ctx, current)
		if err != nil {
			return err
		}
		if reservation.Spec.ControllerWorkerPod.Name != a.workerPodName || reservation.Spec.ControllerWorkerPod.UID != a.workerPodUID {
			return errConfigurationDeviceAdmission
		}
		if _, _, _, err := readRetainedManagementAddressLease(ctx, a.statusReader, a.key.Namespace, ref, op, reservation, a.configurationAdmission.leaseNamespace, time.Now()); err != nil {
			return err
		}
		if err := contract.ReadControllerWorkerBinding(ctx, a.statusReader, reservation, time.Now()); err != nil {
			return err
		}
		return a.configurationAdmission.authorize(ctx, reservation)
	}
	if err := check(ctx); err != nil {
		return false, err
	}
	if complete, err := a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now()); err != nil || !complete {
		return false, err
	}
	verified, err := a.client.verifyManagementAddressChange(ctx, ref, op, endpointProof)
	if err != nil || !verified {
		return false, err
	}
	if err := check(ctx); err != nil {
		return false, err
	}
	return true, nil
}
