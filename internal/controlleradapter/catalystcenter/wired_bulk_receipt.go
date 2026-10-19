// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Stable receipt key excludes transient task/nonce/mask/native baseline evidence,
// while retaining the complete logical and physical batch incarnation.
func wiredBulkReceiptKey(op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.PhysicalGroup == nil || op.Section != "inventory" || op.TargetSerial != "" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.CreatedResourceID != "" || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CancellationRequestedAt != nil {
		return "", errConfigurationTransition
	}
	count, _, _, err := parseWiredBulkResource(op.Resource, op.Action)
	if err != nil {
		return "", err
	}
	targets, err := contract.CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil || count != len(targets) {
		return "", errConfigurationStale
	}
	raw, err := json.Marshal(struct {
		Root, Parent, Action string
		Targets              []contract.GroupTargetIdentity
	}{op.TargetID, op.ParentID, op.Action, targets})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("wired-bulk:%x", sha256.Sum256(raw)), nil
}

// This suppresses only an independently verified current-revision action. Native
// presence alone is never a receipt for re-provisioning. It grants no dispatch,
// release or predecessor-retirement authority.
func wiredBulkAlreadyApplied(state *cvk.NetworkControllerConfigurationStatus, desired *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if desired == nil || desired.Phase != "Prepared" || desired.TaskID != "" || desired.SubmittedAt != nil || contract.ValidateGroupJournalIdentity(desired) != nil {
		return false, errConfigurationStale
	}
	key, err := wiredBulkReceiptKey(desired)
	if err != nil {
		return false, err
	}
	if now.IsZero() || desired.ConfigUID == "" || desired.ConfigName == "" || desired.ConfigGeneration < 1 || desired.ControllerGeneration < 1 {
		return false, errConfigurationTransition
	}
	if state == nil {
		return false, nil
	}
	var receipt *cvk.NetworkControllerConfigurationActionApplication
	for i := range state.ActionApplications {
		r := &state.ActionApplications[i]
		if r.Section == "inventory" && r.Resource == key {
			if receipt != nil {
				return false, errConfigurationOwned
			}
			receipt = r
		}
	}
	if receipt == nil {
		return false, nil
	}
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section == "inventory" {
			if owner.ConfigUID != desired.ConfigUID || owner.ConfigName != desired.ConfigName {
				return false, errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return false, errConfigurationOwned
	}
	old := &receipt.Operation
	if old.ConfigUID != desired.ConfigUID || old.ConfigName != desired.ConfigName {
		return false, errConfigurationOwned
	}
	retainedKey, err := wiredBulkReceiptKey(old)
	if err != nil || retainedKey != key || contract.ValidateGroupJournalIdentity(old) != nil || old.Phase != "Verified" || !validAPIID(old.TaskID) || old.SubmittedAt == nil || old.SubmittedAt.IsZero() || old.SubmittedAt.After(now) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(old.SubmittedAt) || receipt.VerifiedAt.After(now) {
		return false, errConfigurationStale
	}
	if old.ConfigGeneration < 1 || old.ControllerGeneration < 1 {
		return false, errConfigurationStale
	}
	sameRevision := old.ConfigGeneration == desired.ConfigGeneration && old.ControllerGeneration == desired.ControllerGeneration && old.SourceUID == desired.SourceUID && old.SourceVersion == desired.SourceVersion
	if sameRevision && old.Resource != desired.Resource {
		return false, errConfigurationStale
	}
	return sameRevision, nil
}

type wiredBulkConvergenceProof = configurationGroupConvergenceProof

// Native task/record readback is not rendered-device convergence. Both proof
// and exact current managed bindings are mandatory before constructing a final
// receipt; this helper never publishes it or changes a grant/journal itself.
func (c *client) verifiedWiredBulkReceipt(ctx context.Context, reader ctrlclient.Reader, namespace string, batch wiredBulkBatch, op *cvk.NetworkControllerConfigurationOperation, now time.Time, proof wiredBulkConvergenceProof, dependencies ...wiredBulkDependencyObserver) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if reader == nil || namespace == "" || proof == nil || op == nil || op.Phase != "Verifying" || now.IsZero() || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationDeviceAdmission
	}
	key, err := wiredBulkReceiptKey(op)
	if err != nil {
		return nil, err
	}
	collect := func(ctx context.Context) (*wiredBulkSnapshot, []contract.GroupTargetIdentity, time.Time, error) {
		completed, err := c.observeWiredBulkProvisionCompletion(ctx, batch.path, batch.members, op, now, dependencies...)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		native, targets, err := c.wiredBulkTargets(ctx, reader, namespace, batch.path, batch.members, dependencies...)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		return native, targets, completed, nil
	}
	if _, err := observeConfigurationGroupConvergence(ctx, op, now, collect, proof); err != nil {
		return nil, err
	}

	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: key, Operation: *verified, VerifiedAt: &at}, nil
}

// Bind every batch member to the original complete parsed source. Grouping
// options/import ownership remain separate runtime obligations.
func declaredWiredBulkBatch(source *configSourceSnapshot, batch wiredBulkBatch) error {
	if source == nil || len(batch.members) == 0 || len(batch.members) > wiredBulkProvisionLimit || !validSitePath(batch.path) {
		return errConfigurationTransition
	}
	intent, states, _, err := parseWiredBulkSource(source)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, member := range batch.members {
		name := member.desired.reference.Name
		if name == "" || seen[name] || states[name] != member.state || !siteWithin(member.desired.site, batch.path) {
			return errConfigurationStale
		}
		seen[name] = true
		matches := 0
		for _, device := range intent.devices {
			if device.reference.Name == name {
				if !reflect.DeepEqual(device, member.desired) {
					return errConfigurationStale
				}
				matches++
			}
		}
		if matches != 1 {
			return errConfigurationStale
		}
	}
	return nil
}
