// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLANDeviceProofEnvelopeRetainsLegacyIdentity(t *testing.T) {
	// Golden identity from the pre-extraction LAN protocol. A shared helper
	// change must not orphan an existing pending/result-bearing worker request.
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "cc", UID: "controller-uid"}}
	op := &cvk.NetworkControllerConfigurationOperation{ID: "operation", TaskID: "task", PhysicalGroup: &cvk.NetworkControllerConfigurationGroupIdentity{Digest: "group"}}
	member := &ops.ControllerConfigurationReservation{Spec: ops.ControllerConfigurationReservationSpec{Device: ops.ConfigurationReservationReference{Name: "edge", UID: "device-uid"}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "worker-uid"}}}
	completed := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	leaf := lanLinkProofOperationWithCommands(nc, op, member, "result", completed, []string{"show version"})
	const digest = "6d319e334044aefce7fafdacfccb37983088ef3d64f23380d11bf86410cbf64d"
	if leaf.Name != "cc-lan-proof-"+digest[:48] || leaf.Annotations[lanLinkProofBindingAnnotation] != digest || len(leaf.Annotations) != 1 || leaf.Namespace != nc.Namespace || leaf.OwnerReferences[0].UID != nc.UID || leaf.Spec.Operation.Kind != ops.OperationKindShowCommand || leaf.Spec.TTLSecondsAfterFinished == nil || *leaf.Spec.TTLSecondsAfterFinished != 300 {
		t.Fatal("existing LAN worker request identity changed")
	}
}
