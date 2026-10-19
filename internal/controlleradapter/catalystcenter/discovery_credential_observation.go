// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"slices"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// A read-only candidate for the atomic credential/creator receipt writer.
// This is neither a persisted receipt nor a dispatch/replay capability.
type discoveryCredentialCreateObservation struct {
	id, input, operationID, controllerVersion string
	effect                                    discoveryCredentialPublicEffect
}

// Use the same task bytes for successful timing and native UUID correlation.
// A matching name or completed discovery alone never proves credential delivery.
func (c *client) completedDiscoveryTaskResource(ctx context.Context, taskID string, submitted, now time.Time) (string, bool, error) {
	if !validAPIID(taskID) || submitted.IsZero() || now.Before(submitted) {
		return "", false, errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+taskID, nil, 200)
	if err != nil {
		return "", false, err
	}
	done, err := decodeConfigurationTaskComplete(raw, taskID, submitted, now)
	if err != nil || !done {
		return "", false, err
	}
	id, err := decodeDiscoveryTaskResource(raw, taskID)
	return id, err == nil, err
}

// Validate the exact in-flight journal before and after the native reads. No
// credential material is obtained from readback, returned, logged, or hashed.
// The caller must still CAS the eventual receipt against controllerVersion.
func (a *adapter) observeDiscoveryCredentialCreate(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, projection *discoveryCredentialProjection, index int, op *cvk.NetworkControllerConfigurationOperation) (*discoveryCredentialCreateObservation, error) {
	if a == nil || a.client == nil || projection == nil || config == nil || source == nil || op == nil ||
		!configurationOperationID.MatchString(op.ID) || op.Section != "inventory" || op.Action != "Create" || op.Phase != "Verifying" ||
		!validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) ||
		op.TargetID != "" || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil ||
		op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.SSIDCredentialHistory) != 0 ||
		op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation ||
		op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.Generation != op.ControllerGeneration ||
		nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || configurationOwnersMatch(nc.Status.Configuration, config) != nil {
		return nil, errConfigurationStale
	}
	var globals map[string]map[string]globalCredentialMetadata
	if index < 0 || index >= len(projection.intents) {
		return nil, errConfigurationTransition
	}
	if len(projection.intents[index].base.credentials) > 0 {
		globals, err = a.client.globalCredentials(ctx)
		if err != nil {
			return nil, err
		}
	}
	request, input, err := projection.bindRequest(nc, config, source, index, globals)
	if err != nil {
		return nil, err
	}
	history, err := discoveryCredentialExecutionHistory(nc, config, source)
	if err != nil {
		return nil, err
	}
	expectedInput, err := history.input(discoveryPublicDigest(projection.intents[index].base.name), input)
	if err != nil || expectedInput != op.Resource || !slices.Equal(request.versions, op.CredentialVersions) || validateConfigurationCredentialVersions(source, "inventory", op.CredentialVersions) != nil {
		return nil, errConfigurationSecret
	}
	id, done, err := a.client.completedDiscoveryTaskResource(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	actual, err := a.client.discoveries(ctx)
	if err != nil {
		return nil, err
	}
	if err := history.absentRetired(actual); err != nil {
		return nil, err
	}
	got, present := actual[projection.intents[index].base.name]
	if !present {
		return nil, errConfigurationBusy
	}
	if got.ID != id {
		return nil, errConfigurationOwned
	}
	matches, err := discoveryMatches(request.public, got)
	if err != nil {
		return nil, err
	}
	if !matches || got.Condition != "Complete" || got.Status != "Inactive" {
		return nil, errConfigurationBusy
	}
	effect, err := checkedDiscoveryCredentialEffect(request.public, got)
	if err != nil {
		return nil, err
	}
	// Re-resolve existing global aliases to fence replacement during observation.
	if len(projection.intents[index].base.credentials) > 0 {
		globals, err = a.client.globalCredentials(ctx)
		if err != nil {
			return nil, err
		}
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.ResourceVersion != nc.ResourceVersion || fresh.UID != nc.UID || fresh.Generation != nc.Generation || fresh.Spec.Paused ||
		!fresh.DeletionTimestamp.IsZero() || fresh.Status.Configuration == nil || !reflect.DeepEqual(fresh.Status.Configuration.Operation, op) ||
		configurationOwnersMatch(fresh.Status.Configuration, config) != nil {
		return nil, errConfigurationStale
	}
	_, finalInput, err := projection.bindRequest(fresh, config, source, index, globals)
	if err != nil || finalInput != input {
		return nil, errConfigurationStale
	}
	return &discoveryCredentialCreateObservation{id: id, input: expectedInput, operationID: op.ID, controllerVersion: fresh.ResourceVersion, effect: effect}, nil
}
