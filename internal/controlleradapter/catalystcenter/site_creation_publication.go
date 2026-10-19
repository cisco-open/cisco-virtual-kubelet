// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

// Build the complete status before making it durable. In particular, an archive
// without its verified successor must never be written as an intermediate step.
// Native task/attribute verification remains the caller's responsibility.
func siteCreationPublication(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, row cvk.NetworkControllerConfigurationActionApplication) (*cvk.NetworkControllerConfigurationStatus, error) {
	if state == nil || op == nil || op.Phase != "Verifying" || !reflect.DeepEqual(state.Operation, op) || !siteCreateSelected(op) {
		return nil, errConfigurationTransition
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	if !reflect.DeepEqual(row.Operation, *verified) {
		return nil, errConfigurationStale
	}
	if err := siteCreateCapacity(state, op); err != nil {
		return nil, err
	}
	out := state.DeepCopy()
	if siteRecreateSelected(op.Resource) {
		var err error
		out, err = siteArchivePredecessor(state, op)
		if err != nil {
			return nil, err
		}
	}
	out.ActionApplications = append(out.ActionApplications, *row.DeepCopy())
	out.Operation = verified
	if !siteCreatedReceiptPresent(out, verified) {
		return nil, errConfigurationOwned
	}
	if _, err := siteAppliedRecords(out); err != nil {
		return nil, err
	}
	if _, err := siteRetiredRecords(out); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(out.ActionApplications)
	if err != nil || len(out.ActionApplications) > 256 || len(raw) > 256<<10 {
		return nil, errConfigurationBusy
	}
	return out, nil
}

func (a *adapter) persistSiteCreationPublication(ctx context.Context, nc *cvk.NetworkController) error {
	if nc == nil || nc.Status.Configuration == nil || !siteCreatedReceiptPresent(nc.Status.Configuration, nc.Status.Configuration.Operation) {
		return errConfigurationTransition
	}
	expected := nc.Status.Configuration.DeepCopy()
	if err := a.persistConfigurationActionReceipt(ctx, nc); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	// The shared helper checks the new receipt. Recreation also needs every
	// predecessor row and owner retained by the status schema and status writer.
	if stored.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(stored.Status.Configuration, expected) || !siteCreatedReceiptPresent(stored.Status.Configuration, expected.Operation) {
		return errConfigurationStale
	}
	return nil
}
