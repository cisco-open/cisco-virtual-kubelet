// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
)

const activationApprovalProtocolV1 = "activation-approval-v1"

// validatedActivationApproval is a canonical, read-only view of append-only
// activation authority. Construction requires every frozen target to retain
// the exact valid prepared receipt named by the approval. It does not inspect
// live inventory or grant a mutation; those checks belong to admission at the
// start of the activation window.
type validatedActivationApproval struct {
	Hash          string
	Receipts      map[string]opsv1alpha1.IOSXESoftwareRolloutActivationReceipt
	Prepared      map[string]opsv1alpha1.UpgradePreparedReceiptStatus
	PreparedNames map[string]string
}

func applyActivationLeafAnnotations(
	annotations map[string]string,
	authorization *validatedActivationApproval,
	deviceUID string,
) error {
	if authorization == nil {
		return fmt.Errorf("activation authorization is absent")
	}
	reference, ok := authorization.Receipts[deviceUID]
	if !ok {
		return fmt.Errorf("activation authorization omits device UID %q", deviceUID)
	}
	receipt, ok := authorization.Prepared[deviceUID]
	if !ok {
		return fmt.Errorf("prepared receipt for device UID %q is absent", deviceUID)
	}
	annotations[managedprotocol.AnnotationActivationApprovalHash] = authorization.Hash
	annotations[managedprotocol.AnnotationPreparedReceiptHash] = reference.ReceiptHash
	annotations[managedprotocol.AnnotationPreparedUpgradeName] = authorization.PreparedNames[deviceUID]
	annotations[managedprotocol.AnnotationPreparedUpgradeUID] = reference.UpgradeUID
	annotations[managedprotocol.AnnotationPreparedTrustHash] = receipt.TrustIdentityHash
	annotations[managedprotocol.AnnotationPreparedSourceDigest] = receipt.SourceDigest
	return nil
}

func validateActivationLeafAnnotations(
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	authorization *validatedActivationApproval,
	deviceUID string,
) error {
	if leaf == nil {
		return fmt.Errorf("activation leaf is absent")
	}
	expected := map[string]string{}
	if err := applyActivationLeafAnnotations(expected, authorization, deviceUID); err != nil {
		return err
	}
	for key, value := range expected {
		if leaf.Annotations[key] != value {
			return fmt.Errorf("activation leaf %s/%s annotation %q does not match the authorized prepared receipt", leaf.Namespace, leaf.Name, key)
		}
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) revalidateActivationAuthorization(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	expected *validatedActivationApproval,
) error {
	if expected == nil {
		return fmt.Errorf("activation authorization is absent")
	}
	children, err := r.rolloutChildren(ctx, rollout)
	if err != nil {
		return err
	}
	current, err := validateActivationApproval(rollout, children)
	if err != nil {
		return err
	}
	if current.Hash != expected.Hash {
		return fmt.Errorf("activation authorization changed during admission")
	}
	return nil
}

func validateActivationApproval(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	leaves map[string]opsv1alpha1.IOSXESoftwareUpgrade,
) (*validatedActivationApproval, error) {
	if rollout == nil || rollout.Status.FrozenPlan == nil || rollout.Spec.ActivationApproval == nil {
		return nil, fmt.Errorf("activation approval inputs are incomplete")
	}
	if rollout.Spec.Plan.Strategy != opsv1alpha1.IOSXESoftwareRolloutStrategyPrepareOnly {
		return nil, fmt.Errorf("activation approval requires a PrepareOnly plan")
	}
	approval := rollout.Spec.ActivationApproval
	if approval.PlanHash != rollout.Status.FrozenPlan.Hash {
		return nil, fmt.Errorf("activation approval does not name the frozen plan hash")
	}
	if strings.TrimSpace(approval.ApprovedBy) == "" || approval.ApprovedAt.IsZero() ||
		approval.NotBefore.IsZero() || approval.NotAfter.IsZero() ||
		!approval.NotBefore.Time.Before(approval.NotAfter.Time) ||
		approval.ApprovedAt.Time.After(approval.NotAfter.Time) {
		return nil, fmt.Errorf("activation approval identity or UTC window is invalid")
	}
	if len(approval.Receipts) != len(rollout.Status.FrozenPlan.Targets) {
		return nil, fmt.Errorf("activation approval names %d receipts for %d frozen targets",
			len(approval.Receipts), len(rollout.Status.FrozenPlan.Targets))
	}

	receipts := make(map[string]opsv1alpha1.IOSXESoftwareRolloutActivationReceipt, len(approval.Receipts))
	prepared := make(map[string]opsv1alpha1.UpgradePreparedReceiptStatus, len(approval.Receipts))
	preparedNames := make(map[string]string, len(approval.Receipts))
	for _, reference := range approval.Receipts {
		if strings.TrimSpace(reference.DeviceUID) == "" || strings.TrimSpace(reference.UpgradeUID) == "" ||
			!validSHA256Identity(reference.ReceiptHash) {
			return nil, fmt.Errorf("activation approval contains an incomplete receipt reference")
		}
		if _, duplicate := receipts[reference.DeviceUID]; duplicate {
			return nil, fmt.Errorf("activation approval duplicates device UID %q", reference.DeviceUID)
		}
		receipts[reference.DeviceUID] = reference
	}

	for _, target := range rollout.Status.FrozenPlan.Targets {
		reference, ok := receipts[target.DeviceUID]
		if !ok {
			return nil, fmt.Errorf("activation approval omits frozen target device UID %q", target.DeviceUID)
		}
		leaf, ok := leaves[target.ChildName]
		if !ok {
			return nil, fmt.Errorf("prepared leaf %q is absent", target.ChildName)
		}
		if string(leaf.UID) != reference.UpgradeUID || leaf.Status.Phase != opsv1alpha1.UpgradePhasePrepared ||
			leaf.Status.PreparedReceipt == nil {
			return nil, fmt.Errorf("prepared leaf %q identity or phase changed", target.ChildName)
		}
		receipt := leaf.Status.PreparedReceipt
		if err := softwareupgrade.ValidatePreparedReceipt(receipt); err != nil {
			return nil, fmt.Errorf("prepared leaf %q receipt is invalid: %w", target.ChildName, err)
		}
		if receipt.ReceiptHash != reference.ReceiptHash || receipt.UpgradeUID != reference.UpgradeUID ||
			receipt.DeviceUID != target.DeviceUID || receipt.NodeUID != target.NodeUID ||
			receipt.PhysicalIdentity != target.PhysicalIdentity || receipt.DeviceGeneration != target.DeviceGeneration ||
			receipt.CampaignUID != string(rollout.UID) || receipt.PlanHash != rollout.Status.FrozenPlan.Hash ||
			receipt.PolicyUID != rollout.Status.FrozenPlan.Policy.UID ||
			receipt.TargetVersion != rollout.Spec.Plan.TargetVersion ||
			!strings.HasPrefix(receipt.ValidatedVersion, rollout.Spec.Plan.TargetVersion) ||
			receipt.SourceDigest != "sha256:"+rollout.Spec.Plan.Image.SHA256 {
			return nil, fmt.Errorf("prepared leaf %q receipt does not match frozen target, source, or plan", target.ChildName)
		}
		sourceHash, err := softwareupgrade.PreparedSourceIdentityHash(leaf.Spec.ImageSource)
		if err != nil || sourceHash != receipt.SourceIdentityHash {
			return nil, fmt.Errorf("prepared leaf %q source identity no longer matches its receipt", target.ChildName)
		}
		prepared[target.DeviceUID] = *receipt.DeepCopy()
		preparedNames[target.DeviceUID] = target.ChildName
	}

	canonicalReceipts := append([]opsv1alpha1.IOSXESoftwareRolloutActivationReceipt(nil), approval.Receipts...)
	sort.Slice(canonicalReceipts, func(i, j int) bool {
		return canonicalReceipts[i].DeviceUID < canonicalReceipts[j].DeviceUID
	})
	payload := struct {
		Protocol    string                                              `json:"protocol"`
		CampaignUID string                                              `json:"campaignUID"`
		PlanHash    string                                              `json:"planHash"`
		Receipts    []opsv1alpha1.IOSXESoftwareRolloutActivationReceipt `json:"receipts"`
		NotBefore   string                                              `json:"notBefore"`
		NotAfter    string                                              `json:"notAfter"`
		ApprovedBy  string                                              `json:"approvedBy"`
		ApprovedAt  string                                              `json:"approvedAt"`
	}{
		Protocol: activationApprovalProtocolV1, CampaignUID: string(rollout.UID),
		PlanHash: approval.PlanHash, Receipts: canonicalReceipts,
		NotBefore:  approval.NotBefore.UTC().Format("2006-01-02T15:04:05.999999999Z"),
		NotAfter:   approval.NotAfter.UTC().Format("2006-01-02T15:04:05.999999999Z"),
		ApprovedBy: approval.ApprovedBy,
		ApprovedAt: approval.ApprovedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode activation approval: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return &validatedActivationApproval{
		Hash: "sha256:" + hex.EncodeToString(digest[:]), Receipts: receipts, Prepared: prepared, PreparedNames: preparedNames,
	}, nil
}

// activationChildName keeps preparation and activation as distinct immutable
// audit records. The suffix hash prevents collisions if a maximal Kubernetes
// name must be truncated.
func activationChildName(preparedName string) string {
	digest := sha256.Sum256([]byte(preparedName))
	suffix := "-activate-" + hex.EncodeToString(digest[:4])
	maxBase := 253 - len(suffix)
	base := strings.TrimRight(preparedName, "-.")
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-.")
	}
	return base + suffix
}

func boundedActivationRequeue(now, boundary time.Time) time.Duration {
	remaining := boundary.Sub(now)
	if remaining <= 0 {
		return time.Second
	}
	if remaining > time.Minute {
		return time.Minute
	}
	return remaining
}

func activationTarget(target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) opsv1alpha1.IOSXESoftwareRolloutPlannedTarget {
	target.ChildName = activationChildName(target.ChildName)
	return target
}

func isActivationTarget(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) bool {
	if rollout == nil || rollout.Status.FrozenPlan == nil || rollout.Spec.ActivationApproval == nil {
		return false
	}
	for _, frozen := range rollout.Status.FrozenPlan.Targets {
		if frozen.DeviceUID == target.DeviceUID {
			return target.ChildName == activationChildName(frozen.ChildName)
		}
	}
	return false
}

func validSHA256Identity(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && strings.ToLower(value) == value
}
