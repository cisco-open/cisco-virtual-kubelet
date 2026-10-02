// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package softwareupgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

const preparedReceiptProtocolV1 = "prepare-v1"
const preparedContentBindingV1 = "source-digest-install-claim-v1"

type preparedSourceIdentity struct {
	URL                string `json:"url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	URLSecretName      string `json:"urlSecretName,omitempty"`
	ConfigMapName      string `json:"configMapName,omitempty"`
	DeviceFilePath     string `json:"deviceFilePath,omitempty"`
	DeviceFileSHA256   string `json:"deviceFileSHA256,omitempty"`
	LegacyLocalPath    string `json:"legacyLocalPath,omitempty"`
	LegacyLocalSHA256  string `json:"legacyLocalSHA256,omitempty"`
	PreinstalledIntent bool   `json:"preinstalledIntent,omitempty"`
}

// PreparedSourceIdentityHash covers the credential-free image source intent.
// Secret content is represented separately by its immutable Kubernetes UID.
func PreparedSourceIdentityHash(source opsv1alpha1.UpgradeImageSource) (string, error) {
	identity := preparedSourceIdentity{
		URL: source.URL, SHA256: source.SHA256,
		LegacyLocalPath: source.LocalPath, LegacyLocalSHA256: source.LocalPathSHA256,
		PreinstalledIntent: source.Preinstalled != nil,
	}
	if source.URLSecretRef != nil {
		identity.URLSecretName = source.URLSecretRef.Name
	}
	if source.ConfigMapRef != nil {
		identity.ConfigMapName = source.ConfigMapRef.Name
	}
	if source.DeviceFile != nil {
		identity.DeviceFilePath = source.DeviceFile.Path
		identity.DeviceFileSHA256 = source.DeviceFile.SHA256
	}
	return canonicalPreparedHash(identity)
}

// PreparedTrustIdentityHash binds the exact device-access Secret revisions
// loaded by the worker without exposing any credential or certificate bytes.
func PreparedTrustIdentityHash(credential, tls, provisioning string) (string, error) {
	return canonicalPreparedHash(struct {
		Credential   string `json:"credential"`
		TLS          string `json:"tls"`
		Provisioning string `json:"provisioning"`
	}{credential, tls, provisioning})
}

func canonicalPreparedHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode prepared identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// PreparedReceiptHash returns the content address of the canonical receipt
// with ReceiptHash cleared. All fields are fixed-order struct fields, so JSON
// is deterministic and delimiter-bearing values cannot collide.
func PreparedReceiptHash(receipt opsv1alpha1.UpgradePreparedReceiptStatus) (string, error) {
	receipt.ReceiptHash = ""
	receipt.ManagedMutationClaims = append([]opsv1alpha1.UpgradeManagedMutationClaimStatus(nil), receipt.ManagedMutationClaims...)
	sort.Slice(receipt.ManagedMutationClaims, func(i, j int) bool {
		return receipt.ManagedMutationClaims[i].Stage < receipt.ManagedMutationClaims[j].Stage
	})
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("encode prepared receipt: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ValidatePreparedReceipt verifies the self-contained identity and content
// binding. Manager-side activation must additionally compare it with the live
// leaf, device, frozen plan and a separate activation approval.
func ValidatePreparedReceipt(receipt *opsv1alpha1.UpgradePreparedReceiptStatus) error {
	if receipt == nil {
		return fmt.Errorf("prepared receipt is missing")
	}
	if receipt.ProtocolVersion != preparedReceiptProtocolV1 ||
		strings.TrimSpace(receipt.UpgradeUID) == "" ||
		strings.TrimSpace(receipt.DeviceUID) == "" ||
		strings.TrimSpace(receipt.SourceDigest) == "" ||
		strings.TrimSpace(receipt.SourceIdentityHash) == "" ||
		receipt.ContentBinding != preparedContentBindingV1 ||
		strings.TrimSpace(receipt.TargetVersion) == "" ||
		strings.TrimSpace(receipt.ValidatedVersion) == "" ||
		strings.TrimSpace(receipt.RunningVersion) == "" ||
		receipt.InstallStartedAt.IsZero() || receipt.PreparedAt.IsZero() ||
		!receipt.PrimarySupervisorInstalled {
		return fmt.Errorf("prepared receipt identity is incomplete")
	}
	if !validContentDigest(receipt.SourceDigest) {
		return fmt.Errorf("prepared receipt source digest is invalid")
	}
	if receipt.SourceSize < 0 {
		return fmt.Errorf("prepared receipt source size is invalid")
	}
	if receipt.PreparedAt.Time.Before(receipt.InstallStartedAt.Time) {
		return fmt.Errorf("prepared receipt predates its install claim")
	}
	if !versionMatches(receipt.ValidatedVersion, receipt.TargetVersion) ||
		versionMatches(receipt.RunningVersion, receipt.TargetVersion) {
		return fmt.Errorf("prepared receipt version boundary is invalid")
	}
	if (receipt.CampaignUID == "") != (receipt.PlanHash == "") {
		return fmt.Errorf("prepared receipt campaign identity is incomplete")
	}
	if receipt.PlanHash != "" && !validContentDigest(receipt.PlanHash) {
		return fmt.Errorf("prepared receipt plan hash is invalid")
	}
	if !validContentDigest(receipt.SourceIdentityHash) {
		return fmt.Errorf("prepared receipt source identity hash is invalid")
	}
	if receipt.TrustIdentityHash != "" && !validContentDigest(receipt.TrustIdentityHash) {
		return fmt.Errorf("prepared receipt trust identity hash is invalid")
	}
	if receipt.IndividualSupervisorInstall && (!receipt.StandbySupervisorInstalled ||
		strings.TrimSpace(receipt.StandbySupervisorID) == "" || strings.TrimSpace(receipt.StandbyRunningVersion) == "") {
		return fmt.Errorf("prepared receipt standby supervisor evidence is incomplete")
	}
	if !receipt.IndividualSupervisorInstall && (receipt.StandbySupervisorInstalled ||
		receipt.StandbySupervisorID != "" || receipt.StandbyRunningVersion != "") {
		return fmt.Errorf("prepared receipt has unexpected standby supervisor evidence")
	}
	if receipt.CampaignUID != "" {
		if strings.TrimSpace(receipt.NodeUID) == "" || strings.TrimSpace(receipt.PhysicalIdentity) == "" ||
			receipt.DeviceGeneration < 1 || receipt.ManagedProtocolVersion == "" ||
			strings.TrimSpace(receipt.PolicyUID) == "" || strings.TrimSpace(receipt.PolicyResourceVersion) == "" ||
			receipt.PolicyEpoch < 1 || !validContentDigest(receipt.WorkerRevision) ||
			!validContentDigest(receipt.TrustIdentityHash) || len(receipt.ManagedMutationClaims) == 0 {
			return fmt.Errorf("prepared receipt managed identity is incomplete")
		}
		var stagingClaim, primaryClaim, standbyClaim bool
		for _, claim := range receipt.ManagedMutationClaims {
			if claim.Stage != opsv1alpha1.UpgradeManagedMutationStaging &&
				claim.Stage != opsv1alpha1.UpgradeManagedMutationPrimaryInstall &&
				claim.Stage != opsv1alpha1.UpgradeManagedMutationStandbyInstall {
				return fmt.Errorf("prepared receipt contains non-install mutation claim %q", claim.Stage)
			}
			if claim.ReservationID == "" || claim.PolicyEpoch != receipt.PolicyEpoch || claim.ClaimedAt.IsZero() {
				return fmt.Errorf("prepared receipt mutation claim identity is incomplete")
			}
			switch claim.Stage {
			case opsv1alpha1.UpgradeManagedMutationStaging:
				stagingClaim = true
			case opsv1alpha1.UpgradeManagedMutationPrimaryInstall:
				primaryClaim = true
			case opsv1alpha1.UpgradeManagedMutationStandbyInstall:
				standbyClaim = true
			}
		}
		if !stagingClaim && !primaryClaim {
			return fmt.Errorf("prepared receipt lacks a primary install or staging claim")
		}
		if receipt.IndividualSupervisorInstall && !standbyClaim {
			return fmt.Errorf("prepared receipt lacks a standby install claim")
		}
	}
	want, err := PreparedReceiptHash(*receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptHash != want {
		return fmt.Errorf("prepared receipt hash mismatch")
	}
	return nil
}
