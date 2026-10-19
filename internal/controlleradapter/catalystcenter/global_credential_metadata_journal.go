// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
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

func metadataCredentialOperationReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || !validMetadataCredentialOperation(op, "Verified", time.Now()) {
		return false
	}
	key, _ := metadataCredentialKey(op.Resource)
	count := 0
	for _, receipt := range state.ActionApplications {
		if receipt.Section != "network_settings" || !strings.HasPrefix(receipt.Resource, key+":") {
			continue
		}
		count++
		if _, err := metadataCredentialReceiptTarget(&receipt, time.Now()); err != nil || !reflect.DeepEqual(receipt.Operation, *op) {
			return false
		}
	}
	return count == 1
}

// Verify native state again at the persistence boundary and atomically retain
// the exact UUID with Verified. Reuse the shared writer and pruning detection;
// this path never fabricates a versioned credential receipt.
func (a *adapter) recordMetadataCredentialApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, id string) error {
	if source == nil || config == nil || !configurationOperationID.MatchString(id) || !supportedConfigurationApply(config) ||
		len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "network_settings" {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	state, op := nc.Status.Configuration, nc.Status.Configuration.Operation
	if op.ID != id || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation ||
		op.SourceUID != string(source.uid) || op.SourceVersion != source.version || config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return errConfigurationStale
	}
	owned := false
	for _, owner := range state.Owners {
		if owner.Section == "network_settings" && owner.ConfigName == config.Name && owner.ConfigUID == string(config.UID) {
			owned = true
		}
	}
	if !owned {
		return errConfigurationOwned
	}
	wants, _, err := splitNetworkCredentialConfiguration(source.raw)
	if err != nil {
		return err
	}
	e := &globalCredentialExecutor{client: a.client, desired: wants, configName: config.Name, owner: string(config.UID), receipts: state.CredentialApplications, metadataReceipts: state.ActionApplications}
	target, verified, err := e.verifyMetadataCredential(ctx, op, time.Now())
	if err != nil {
		return err
	}
	if !verified || !validAPIID(target) {
		return errConfigurationBusy
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	key, _ := metadataCredentialKey(op.Resource)
	index := -1
	for i, receipt := range state.ActionApplications {
		if receipt.Section == "network_settings" && strings.HasPrefix(receipt.Resource, key+":") {
			if index >= 0 || receipt.Resource != key+":"+target {
				return errConfigurationOwned
			}
			index = i
		}
	}
	if index < 0 && len(state.ActionApplications) >= 256 {
		return errConfigurationTransition
	}
	op.Phase = "Verified"
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "network_settings", Resource: key + ":" + target, Operation: *op.DeepCopy(), VerifiedAt: &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}}
	if index < 0 {
		state.ActionApplications = append(state.ActionApplications, receipt)
	} else {
		state.ActionApplications[index] = receipt
	}
	if !metadataCredentialOperationReceiptPresent(state, op) {
		return errConfigurationStale
	}
	return a.persistConfigurationActionReceipt(ctx, nc)
}
