// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"errors"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errPnPRegistrationCapacity = errors.New("PnP registration ownership receipt capacity is insufficient")

// Reserve the whole remaining declared cohort in the existing serialized ledger.
// Other sections' receipts consume the same space; no entry may be evicted.
// UUIDs are unknown before POST, so budget their complete native ID bound.
func pnpRegistrationCohortCapacity(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, selected map[string]string) error {
	if nc == nil || config == nil {
		return errConfigurationTransition
	}
	pending := 0
	for _, id := range selected {
		if id == "" {
			pending++
		}
	}
	if pending == 0 {
		return nil
	}
	var receipts []cvk.NetworkControllerConfigurationActionApplication
	if nc.Status.Configuration != nil {
		receipts = append(receipts, nc.Status.Configuration.ActionApplications...)
	}
	if pending > 256-len(receipts) {
		return errPnPRegistrationCapacity
	}
	now := metav1.Now()
	for resource, id := range selected {
		if id != "" {
			continue
		}
		op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("f", 32), Section: "inventory", Resource: resource, Action: "Create", Phase: "Verified", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, CreatedResourceID: strings.Repeat("x", 128), SubmittedAt: &now}
		if source != nil {
			op.SourceUID = string(source.uid)
			op.SourceVersion = source.version
		}
		receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "pnp-record:" + op.CreatedResourceID, Operation: op, VerifiedAt: &now})
	}
	return pnpRegistrationReceiptBytesFit(receipts)
}

func pnpRegistrationReceiptBytesFit(receipts []cvk.NetworkControllerConfigurationActionApplication) error {
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errPnPRegistrationCapacity
	}
	return nil
}

// Recheck actual publication after native proof and before the common atomic
// Verified/receipt CAS. The caller's source/journal bracket fences concurrent edits.
func pnpRegistrationPublicationCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || op == nil {
		return errConfigurationTransition
	}
	future := op.DeepCopy()
	future.Phase = "Verified"
	if !validPnPRegistrationReceiptOperation(future) {
		return errConfigurationTransition
	}
	receipts := append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...)
	resource := "pnp-record:" + future.CreatedResourceID
	for _, r := range receipts {
		if r.Operation.ID == op.ID || r.Section == "inventory" && r.Resource == resource {
			return errConfigurationOwned
		}
	}
	now := metav1.Now()
	receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: resource, Operation: *future, VerifiedAt: &now})
	return pnpRegistrationReceiptBytesFit(receipts)
}
