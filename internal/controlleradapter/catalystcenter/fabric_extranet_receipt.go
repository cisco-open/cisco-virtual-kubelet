// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func extranetReceiptPrefix(name string) (string, error) {
	if !validProfileName(name) {
		return "", errInvalidResponse
	}
	return fmt.Sprintf("extranet-policy:%x:", sha256.Sum256([]byte(name))), nil
}
func validExtranetCreateResource(resource string) bool {
	digest, ok := strings.CutPrefix(resource, "extranet-create:")
	if !ok || len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func extranetExpansionReceiptResource(nativeID string) (string, error) {
	if !validAPIID(nativeID) {
		return "", errInvalidResponse
	}
	return "extranet-expansion:" + nativeID, nil
}

// Build evidence using the existing action-receipt type. This performs no
// Kubernetes write and grants no dispatch authority. The shared group writer
// atomically persists this receipt with its Verified operation under CAS.
// The receipt key binds the logical name and independently observed native UUID;
// the immutable prepared operation must not be rewritten with a post-create ID.
func (c *client) verifiedExtranetReceipt(ctx context.Context, want fabricExtranetIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if op == nil || op.Section != "fabric" || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	var verifiedSnapshot *fabricExtranetSnapshot
	var err error
	switch op.Action {
	case "Create":
		if !validExtranetCreateResource(op.Resource) {
			return nil, errConfigurationTransition
		}
		verifiedSnapshot, err = c.verifiedExtranetSnapshot(ctx, want, op, now)
	case "Update":
		verifiedSnapshot, err = c.verifiedExtranetExpansionSnapshot(ctx, want, op, now)
	default:
		return nil, errConfigurationTransition
	}
	if err != nil {
		return nil, err
	}
	if verifiedSnapshot == nil {
		return nil, errConfigurationBusy
	}
	// Resolve readback and footprint again as one receipt snapshot. A late policy
	// deletion/replacement of dependencies cannot be hidden by prior verification.
	snapshot, err := c.extranetSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	resource := snapshot.resource()
	operationResource := op.Resource
	if op.Action == "Update" {
		resource = snapshot.updateResource(op.TargetID)
		operationResource, err = extranetUpdateNativeResource(op.Resource)
		if err != nil {
			return nil, err
		}
	}
	if resource != operationResource || len(snapshot.physical) != len(op.PhysicalGroup.Targets) {
		return nil, errConfigurationStale
	}
	physical := map[string]string{}
	for _, d := range snapshot.physical {
		physical[d.ID] = d.Serial
	}
	for _, target := range op.PhysicalGroup.Targets {
		if physical[target.InventoryID] != target.PhysicalIdentity {
			return nil, errConfigurationStale
		}
	}
	observed, exists := snapshot.policies[want.policy.Name]
	if !exists || observed.ID != verifiedSnapshot.policies[want.policy.Name].ID || !extranetPolicyMatches(snapshot.desired, observed) {
		return nil, errConfigurationStale
	}
	prefix, err := extranetReceiptPrefix(want.policy.Name)
	if err != nil {
		return nil, err
	}
	receiptKey := prefix + observed.ID
	if op.Action == "Update" {
		// Completion evidence is separate from the original ownership receipt. A
		// stable native-policy key bounds new history to the latest update. The
		// shared replacement writer requires retirement of its predecessor first.
		receiptKey, err = extranetExpansionReceiptResource(observed.ID)
		if err != nil {
			return nil, err
		}
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: receiptKey, Operation: *verified, VerifiedAt: &at}, nil
}

// Receipt provenance is separate from current desired-state compliance and
// mutation admission. Source generations can change without transferring native
// ownership, but a recreated config or a replaced native policy cannot inherit it.
func ownedExtranetPolicy(state *cvk.NetworkControllerConfigurationStatus, configName, configUID, name, nativeID string, now time.Time) (bool, error) {
	prefix, err := extranetReceiptPrefix(name)
	if err != nil || configName == "" || configUID == "" || !validAPIID(nativeID) {
		return false, errConfigurationTransition
	}
	if state == nil {
		return false, nil
	}
	var found *cvk.NetworkControllerConfigurationActionApplication
	for i := range state.ActionApplications {
		receipt := &state.ActionApplications[i]
		if receipt.Section != "fabric" || !strings.HasPrefix(receipt.Resource, prefix) {
			continue
		}
		if found != nil {
			return false, errConfigurationOwned
		}
		found = receipt
	}
	if found == nil {
		return false, nil
	}
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section != "fabric" {
			continue
		}
		if owner.ConfigName != configName || owner.ConfigUID != configUID {
			return false, errConfigurationOwned
		}
		owners++
	}
	if owners != 1 {
		return false, errConfigurationOwned
	}
	op := &found.Operation
	if op.ConfigName != configName || op.ConfigUID != configUID {
		return false, errConfigurationOwned
	}
	if found.Resource != prefix+nativeID {
		return false, errConfigurationStale
	}
	if op.Section != "fabric" || op.Phase != "Verified" || op.Action != "Create" || op.TargetID != "" || op.ParentID != "" || !validExtranetCreateResource(op.Resource) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || found.VerifiedAt == nil || found.VerifiedAt.IsZero() || found.VerifiedAt.Before(op.SubmittedAt) || found.VerifiedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil {
		return false, errConfigurationStale
	}
	return true, nil
}
