// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadIntentBinding checks current pre-dispatch intent without loading any
// credential Secret. It is one authorization gate, not a submission permit.
// The adapter still validates the exact model/schema and controller-worker
// incarnation. Readers must be uncached. Credential-bearing physical actions
// remain unsupported by this reservation protocol.
func ReadIntentBinding(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, controllerType cvk.NetworkControllerType) error {
	if r == nil || r.UID == "" {
		return errors.New("configuration reservation incarnation is unavailable")
	}
	return readCandidateIntentBinding(ctx, reader, r, controllerType)
}

// Candidate reads validate the same spec/source before the API assigns a UID.
func readCandidateIntentBinding(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, controllerType cvk.NetworkControllerType) error {
	return readIntentAtPhase(ctx, reader, r, controllerType, "Prepared")
}

func readIntentAtPhase(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, controllerType cvk.NetworkControllerType, phase string) error {
	reject := errors.New("configuration reservation intent is no longer authorized")
	if reader == nil || r == nil || controllerType == "" {
		return reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if string(nc.UID) != r.Spec.Controller.UID || nc.UID == "" || nc.Generation != r.Spec.ControllerGeneration || nc.Spec.Type != controllerType || nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return reject
	}
	op := nc.Status.Configuration.Operation
	if op.Phase != phase || validateOperationSpec(r, op) != nil {
		return reject
	}
	return readBoundConfigurationIntent(ctx, reader, &nc, r)
}

// Both reservation formats independently bind their journal before using this
// shared source/ownership check. It neither grants permission nor reads secrets.
func readBoundConfigurationIntent(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation) error {
	return readBoundConfigurationSource(ctx, reader, nc, r.Namespace, r.Spec.Config, r.Spec.ConfigGeneration)
}

// Source/owner validation shared by managed reservations and read-only bootstrap
// identity observations. Each caller independently binds its operation first.
func readBoundConfigurationSource(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, namespace string, ref ops.ConfigurationReservationReference, generation int64) error {
	reject := errors.New("configuration reservation intent is no longer authorized")
	op := nc.Status.Configuration.Operation
	var intent config.NetworkControllerConfig
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &intent); err != nil {
		return err
	}
	if string(intent.UID) != ref.UID || intent.UID == "" || intent.Generation != generation || intent.DeletionTimestamp != nil || intent.Spec.ControllerRef.Name != nc.Name || intent.Spec.Mode != config.NetworkControllerApplyModeApply || (intent.Spec.Scope != "" && intent.Spec.Scope != "global") || !intent.Spec.ModelSource.Resolved || intent.Spec.ModelSource.ModelVersion == "" || len(intent.Spec.SecretRefs) != 0 {
		return reject
	}
	owns := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == op.Section {
			if owner.ConfigUID != op.ConfigUID || owner.ConfigName != op.ConfigName {
				return reject
			}
			owns++
		}
	}
	sections := 0
	for _, section := range intent.Spec.ManagedSections {
		if section == op.Section {
			sections++
		}
	}
	if owns != 1 || sections != 1 {
		return reject
	}
	source := intent.Spec.Source
	if (source.Inline == nil) == (source.ConfigMapRef == nil) {
		return reject
	}
	if source.ConfigMapRef != nil {
		ref := source.ConfigMapRef
		if ref.Name == "" || ref.Key == "" || op.SourceUID == "" || op.SourceVersion == "" {
			return reject
		}
		var cm core.ConfigMap
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &cm); err != nil {
			return err
		}
		if string(cm.UID) != op.SourceUID || cm.ResourceVersion != op.SourceVersion || cm.DeletionTimestamp != nil {
			return reject
		}
		if _, present := cm.Data[ref.Key]; !present {
			return reject
		}
	} else if op.SourceUID != "" || op.SourceVersion != "" {
		return reject
	}
	// Detect declaration changes while reading the source. Consumers recheck at
	// later boundaries; cross-object reads are not an atomic snapshot.
	var freshIntent config.NetworkControllerConfig
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&intent), &freshIntent); err != nil {
		return err
	}
	if freshIntent.UID != intent.UID || freshIntent.ResourceVersion != intent.ResourceVersion {
		return reject
	}
	var freshController cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(nc), &freshController); err != nil {
		return err
	}
	if freshController.UID != nc.UID || freshController.ResourceVersion != nc.ResourceVersion {
		return reject
	}
	return nil
}
