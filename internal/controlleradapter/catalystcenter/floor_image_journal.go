// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func isFloorImageOperation(section, resource string) bool {
	return section == "sites" && (strings.HasPrefix(resource, "floor-image:") || strings.HasPrefix(resource, "floor-image-sync:"))
}

// Keep the upload receipt and terminal journal state in one Kubernetes write.
// This is a write-only asset completion path; generic attribute verification
// must not bypass its retained evidence requirements.
func (a *adapter) recordFloorImageApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, id string) error {
	if source == nil || config == nil || !configurationOperationID.MatchString(id) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err = a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	state, op := nc.Status.Configuration, nc.Status.Configuration.Operation
	if op.ID != id || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	key, err := floorImageReceiptKey(op.Resource)
	if err != nil {
		return err
	}
	owned := false
	for _, owner := range state.Owners {
		if owner.Section == "sites" && owner.ConfigUID == op.ConfigUID && owner.ConfigName == op.ConfigName {
			owned = true
		}
	}
	if !owned {
		return errConfigurationOwned
	}
	_, _, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil {
		return err
	}
	executor := &floorImageExecutor{client: a.client, desired: images, configName: config.Name, configUID: string(config.UID), receipts: state.ActionApplications}
	// Validate all existing evidence for the selected image before replacing it.
	found := false
	for _, want := range images {
		if want.matchesOperation(op.Resource) {
			found = true
			if _, err = floorImageApplied(want, op.TargetID, op.ConfigName, op.ConfigUID, state.ActionApplications, time.Now()); err != nil {
				return err
			}
			if err := floorImageNewDeclaration(want, state.ActionApplications, op, time.Now()); err != nil {
				return err
			}
		}
	}
	if !found {
		return errConfigurationStale
	}
	index := -1
	for i, receipt := range state.ActionApplications {
		receiptKey, _, err := floorImageRowKey(receipt, time.Now())
		if err != nil {
			return err
		}
		if receiptKey == key {
			if index >= 0 {
				return errConfigurationOwned
			}
			index = i
		}
	}
	if index < 0 && len(state.ActionApplications) >= 256 {
		return errConfigurationTransition
	}
	complete, err := executor.uploadCompleted(ctx, op)
	if err != nil {
		return err
	}
	if !complete {
		return errConfigurationBusy
	}
	if err = a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	op.Phase = "Verified"
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: key, Operation: *op.DeepCopy(), VerifiedAt: &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}}
	if index < 0 {
		state.ActionApplications = append(state.ActionApplications, receipt)
	} else {
		state.ActionApplications[index] = receipt
	}
	return a.persistConfigurationActionReceipt(ctx, nc)
}

func floorImageOperationReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || op == nil || op.Section != "sites" || op.Phase != "Verified" || op.Action != "Update" || op.ConfigName == "" || op.ConfigUID == "" || !configurationOperationID.MatchString(op.ID) || !floorImageCompletionKindMatches(op) || !validAPIID(op.TargetID) || op.TargetSerial != "" || op.ParentID != "" || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
		return false
	}
	key, err := floorImageReceiptKey(op.Resource)
	if err != nil {
		return false
	}
	count := 0
	for _, receipt := range state.ActionApplications {
		if receipt.Section != "sites" || receipt.Resource != key {
			continue
		}
		count++
		if !reflect.DeepEqual(receipt.Operation, *op) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || op.SubmittedAt == nil || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(time.Now()) {
			return false
		}
	}
	return count == 1
}
