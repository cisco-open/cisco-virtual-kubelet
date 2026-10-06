// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package softwareupgrade

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestPreparedReceiptHashAndValidation(t *testing.T) {
	receipt := opsv1alpha1.UpgradePreparedReceiptStatus{
		ProtocolVersion:            preparedReceiptProtocolV1,
		UpgradeUID:                 "upgrade-uid",
		DeviceUID:                  "device-uid",
		SourceDigest:               "sha256:" + strings.Repeat("b", 64),
		SourceSize:                 1024,
		SourceIdentityHash:         "sha256:" + strings.Repeat("c", 64),
		ContentBinding:             preparedContentBindingV1,
		TargetVersion:              "17.18.03",
		ValidatedVersion:           "17.18.03.0.123",
		RunningVersion:             "17.18.02",
		PrimarySupervisorInstalled: true,
		InstallStartedAt:           metav1.NewTime(time.Unix(100, 0).UTC()),
		PreparedAt:                 metav1.NewTime(time.Unix(123, 0).UTC()),
	}
	hash, err := PreparedReceiptHash(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptHash = hash
	if err := ValidatePreparedReceipt(&receipt); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}

	tampered := receipt
	tampered.ValidatedVersion = "17.18.03.0.999"
	if err := ValidatePreparedReceipt(&tampered); err == nil {
		t.Fatal("tampered receipt accepted")
	}

	incomplete := receipt
	incomplete.CampaignUID = "campaign-uid"
	incomplete.ReceiptHash, _ = PreparedReceiptHash(incomplete)
	if err := ValidatePreparedReceipt(&incomplete); err == nil {
		t.Fatal("partial manager identity accepted")
	}
}

func TestPreparedReceiptHashCanonicalizesClaimOrder(t *testing.T) {
	base := opsv1alpha1.UpgradePreparedReceiptStatus{
		ProtocolVersion: preparedReceiptProtocolV1,
		UpgradeUID:      "upgrade-uid", DeviceUID: "device-uid",
		SourceDigest:       "sha256:" + strings.Repeat("a", 64),
		SourceIdentityHash: "sha256:" + strings.Repeat("b", 64),
		ContentBinding:     preparedContentBindingV1,
		TargetVersion:      "17.18.03", ValidatedVersion: "17.18.03.0.123", RunningVersion: "17.18.02",
		PrimarySupervisorInstalled: true,
		InstallStartedAt:           metav1.NewTime(time.Unix(100, 0).UTC()),
		PreparedAt:                 metav1.NewTime(time.Unix(123, 0).UTC()),
		ManagedMutationClaims: []opsv1alpha1.UpgradeManagedMutationClaimStatus{
			{Stage: opsv1alpha1.UpgradeManagedMutationStandbyInstall},
			{Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall},
		},
	}
	reversed := base
	reversed.ManagedMutationClaims = append([]opsv1alpha1.UpgradeManagedMutationClaimStatus(nil), base.ManagedMutationClaims...)
	reversed.ManagedMutationClaims[0], reversed.ManagedMutationClaims[1] =
		reversed.ManagedMutationClaims[1], reversed.ManagedMutationClaims[0]
	left, err := PreparedReceiptHash(base)
	if err != nil {
		t.Fatal(err)
	}
	right, err := PreparedReceiptHash(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("claim ordering changed receipt hash: %s != %s", left, right)
	}
	if base.ManagedMutationClaims[0].Stage != opsv1alpha1.UpgradeManagedMutationStandbyInstall {
		t.Fatal("hashing mutated the caller's claim order")
	}
}

func TestPreparedSourceIdentityHashCoversLocatorAndDigest(t *testing.T) {
	source := opsv1alpha1.UpgradeImageSource{URL: "https://images.example.test/cat9k.bin", SHA256: strings.Repeat("a", 64)}
	first, err := PreparedSourceIdentityHash(source)
	if err != nil {
		t.Fatal(err)
	}
	source.URL = "https://mirror.example.test/cat9k.bin"
	second, err := PreparedSourceIdentityHash(source)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !validContentDigest(first) || !validContentDigest(second) {
		t.Fatalf("source identity hashes do not distinguish locators: %q %q", first, second)
	}
}

func TestManagedPreparedReceiptRequiresFrozenAuthority(t *testing.T) {
	claimTime := metav1.NewTime(time.Unix(100, 0).UTC())
	receipt := opsv1alpha1.UpgradePreparedReceiptStatus{
		ProtocolVersion: preparedReceiptProtocolV1,
		UpgradeUID:      "upgrade-uid", DeviceUID: "device-uid", NodeUID: "node-uid",
		PhysicalIdentity: "serial-1", DeviceGeneration: 7,
		CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
		ManagedProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
		PolicyUID:              "policy-uid", PolicyResourceVersion: "42", PolicyEpoch: 3,
		WorkerRevision:     "sha256:" + strings.Repeat("b", 64),
		SourceDigest:       "sha256:" + strings.Repeat("c", 64),
		SourceIdentityHash: "sha256:" + strings.Repeat("d", 64),
		TrustIdentityHash:  "sha256:" + strings.Repeat("e", 64),
		ContentBinding:     preparedContentBindingV1,
		TargetVersion:      "17.18.03", ValidatedVersion: "17.18.03.0.123", RunningVersion: "17.18.02",
		PrimarySupervisorInstalled: true, InstallStartedAt: claimTime,
		ManagedMutationClaims: []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
			Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall, ReservationID: "reservation-1",
			PolicyEpoch: 3, ControlRevision: 2, ClaimedAt: claimTime,
		}},
		PreparedAt: metav1.NewTime(time.Unix(123, 0).UTC()),
	}
	var err error
	receipt.ReceiptHash, err = PreparedReceiptHash(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreparedReceipt(&receipt); err != nil {
		t.Fatalf("valid managed receipt rejected: %v", err)
	}

	missingTrust := receipt
	missingTrust.TrustIdentityHash = ""
	missingTrust.ReceiptHash, _ = PreparedReceiptHash(missingTrust)
	if err := ValidatePreparedReceipt(&missingTrust); err == nil {
		t.Fatal("managed receipt without trust identity accepted")
	}
}
