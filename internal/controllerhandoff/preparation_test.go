// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerhandoff

import (
	"context"
	"crypto/sha256"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestPreparationPolicyIsPinnedAndBounded(t *testing.T) {
	raw := `{"profile":"RetiredCVKImageArchivesV1","requiredFreeBytes":4000000000,"headroomBytes":100000000,"maxFiles":1,"maxBytes":2000000000}`
	yes := true
	for _, kind := range []string{"valid", "mutable", "uid", "hash", "unlabeled", "commands", "unbounded"} {
		t.Run(kind, func(t *testing.T) {
			cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "test", UID: "uid", Labels: map[string]string{"ops.cisco.vk/swim-preparation-policy": "true"}}, Immutable: &yes, Data: map[string]string{"policy.json": raw}}
			ref := &ops.SWIMPreparationPolicyRef{Name: "policy", UID: "uid", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))}
			switch kind {
			case "mutable":
				cm.Immutable = nil
			case "uid":
				ref.UID = "replacement"
			case "hash":
				cm.Data["policy.json"] += " "
			case "unlabeled":
				cm.Labels = nil
			case "commands":
				cm.Data["policy.json"] = `{"command":"delete flash:*"}`
				ref.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(cm.Data["policy.json"])))
			case "unbounded":
				cm.Data["policy.json"] = `{"profile":"RetiredCVKImageArchivesV1","requiredFreeBytes":1,"maxFiles":999,"maxBytes":1}`
				ref.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(cm.Data["policy.json"])))
			}
			scheme := runtime.NewScheme()
			_ = core.AddToScheme(scheme)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			_, err := ReadPreparationPolicy(context.Background(), c, "test", ref)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("unexpected policy result: %v", err)
			}
		})
	}
}

func TestPreparationReservesDistributionBeforeMutation(t *testing.T) {
	p := PreparationPolicy{RequiredFreeBytes: 2936012800, HeadroomBytes: 134217728, DistributionReserveBytes: 2800000000}
	before, err := p.ForStage("ReadyToDistribute")
	if err != nil {
		t.Fatal(err)
	}
	after, err := p.ForStage("ReadyToActivate")
	if err != nil {
		t.Fatal(err)
	}
	if before.RequiredFreeBytes+before.HeadroomBytes != 5870230528 || after.RequiredFreeBytes+after.HeadroomBytes != 3070230528 {
		t.Fatal("incorrect storage budget")
	}
	if _, err := p.ForStage("unknown"); err == nil {
		t.Fatal("unknown stage accepted")
	}
	up := &ops.IOSXESoftwareUpgrade{Spec: ops.IOSXESoftwareUpgradeSpec{ImageSource: ops.UpgradeImageSource{CatalystCenter: &ops.CatalystCenterImageSource{StandardReloadProfile: "CatalystCenter323"}}}}
	if ExecutionModel(up) != ops.UpgradeExecutionModelCatalystCenterReloadV1 || ops.RequiredManagedUpgradeProtocol(up.Spec) != ops.ManagedUpgradeProtocolControllerReloadV1 {
		t.Fatal("old worker protocol selected")
	}
}
