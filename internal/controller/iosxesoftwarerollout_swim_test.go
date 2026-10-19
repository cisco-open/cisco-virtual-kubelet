// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"strings"
	"testing"
)

func TestControllerRolloutLeafUsesExplicitExecutionSource(t *testing.T) {
	target := policyFenceTarget("edge-a", "device-uid", "campaign-edge-a")
	rollout := policyFenceRollout([]ops.IOSXESoftwareRolloutPlannedTarget{target})
	source := &ops.CatalystCenterImageSource{ControllerName: "catc", ControllerUID: "controller-uid", DeviceID: "device-id", ImageID: "image-id", ImageVersion: "17.18.04.0.759"}
	target.Source.CatalystCenter = source
	target.Source.URL = ""
	target.Source.SecretName = ""
	target.Source.SecretUID = ""
	rollback := false
	rollout.Spec.Plan.RollbackOnFailure = &rollback
	leaf := expectedLeafSpec(rollout, target)
	if leaf.ImageSource.CatalystCenter == nil || leaf.ImageSource.URL != "" || leaf.ImageSource.SHA256 != "" || leaf.ImageSource.URLSecretRef != nil {
		t.Fatalf("controller source became direct intent: %+v", leaf.ImageSource)
	}
	if ops.RequiredManagedUpgradeProtocol(leaf) != ops.ManagedUpgradeProtocolControllerSWIMV1 {
		t.Fatal("old worker protocol admitted SWIM")
	}
	leaf.ImageSource.CatalystCenter.DeviceID = "changed"
	if source.DeviceID != "device-id" {
		t.Fatal("leaf aliases frozen controller source")
	}
	direct := leaf
	direct.ImageSource = ops.UpgradeImageSource{URL: "https://example.test/image"}
	if ops.RequiredManagedUpgradeProtocol(direct) == ops.ManagedUpgradeProtocolControllerSWIMV1 {
		t.Fatal("direct execution inherited SWIM protocol")
	}
}

func TestPreparationRequiresNewWorkerAndDeepCopiesPolicy(t *testing.T) {
	target := policyFenceTarget("edge-a", "device-uid", "campaign-edge-a")
	rollout := policyFenceRollout([]ops.IOSXESoftwareRolloutPlannedTarget{target})
	source := &ops.CatalystCenterImageSource{ControllerName: "catc", ControllerUID: "controller-uid", DeviceID: "device-id", ImageID: "image-id", ImageVersion: "17.18.04.0.759", Preparation: &ops.SWIMPreparationPolicyRef{Name: "policy", UID: "policy-uid", SHA256: strings.Repeat("a", 64)}}
	target.Source.CatalystCenter = source
	leaf := expectedLeafSpec(rollout, target)
	if ops.RequiredManagedUpgradeProtocol(leaf) != ops.ManagedUpgradeProtocolControllerPreparationV1 {
		t.Fatal("older worker admitted automatic remediation")
	}
	leaf.ImageSource.CatalystCenter.Preparation.UID = "replacement"
	if source.Preparation.UID != "policy-uid" {
		t.Fatal("leaf aliases frozen preparation policy")
	}
}
