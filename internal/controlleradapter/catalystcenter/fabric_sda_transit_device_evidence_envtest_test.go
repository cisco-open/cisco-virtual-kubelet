// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"errors"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actual DeviceOperation API identity, generation and metadata/status separation.
// Manager/worker binding and results are simulated; the owner/group inputs here
// are fixture identities, not an installed actor or full SDA convergence proof.
func TestEnvtestSDATransitEvidenceEnvelope(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/ops.cisco.vk_deviceoperations.yaml")
	if err := ops.AddToScheme(api.Scheme()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const namespace = "sda-device-evidence"
	if err := api.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	a := &adapter{statusReader: api, statusWriter: api}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "cc", UID: "fixture-controller"}}
	op := &cvk.NetworkControllerConfigurationOperation{ID: "operation", TaskID: "task", PhysicalGroup: &cvk.NetworkControllerConfigurationGroupIdentity{Digest: "group"}}
	member := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}, Spec: ops.ControllerConfigurationReservationSpec{Device: ops.ConfigurationReservationReference{Name: "edge0", UID: "device-uid"}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "worker-uid"}, DeviceWorkerUsername: "system:serviceaccount:" + namespace + ":network"}}
	completed := time.Now().Add(-10 * time.Second).UTC().Truncate(time.Second)
	expected := configurationDeviceProofOperation(nc, op, member, "fixture-native-result", completed, sdaTransitEvidenceCommands(), "cc-sda-evidence-", sdaTransitEvidenceAnnotation)
	read := func() ([]string, error) {
		return a.readConfigurationDeviceProof(ctx, expected, member, sdaTransitEvidenceAnnotation, completed, time.Now(), errSDATransitDeviceEvidencePending)
	}
	for i := 0; i < 2; i++ {
		if result, err := read(); !errors.Is(err, errSDATransitDeviceEvidencePending) || result != nil {
			t.Fatal("unbound API request accepted", err)
		}
	}
	var list ops.DeviceOperationList
	if err := api.List(ctx, &list, ctrlclient.InNamespace(namespace)); err != nil || len(list.Items) != 1 {
		t.Fatal("API request duplicated", err)
	}
	leaf := list.Items[0].DeepCopy()
	uid, generation, created := leaf.UID, leaf.Generation, leaf.CreationTimestamp
	if uid == "" || generation != 1 || !created.After(completed) {
		t.Fatal("missing fresh API identity")
	}
	now := time.Now().UTC()
	stampLANProofEnvelope(leaf, member, created.Time, created.Time, now)
	leaf.UID, leaf.Generation, leaf.CreationTimestamp = uid, generation, created
	leaf.Status.Conditions[0].Reason = "FixtureSDAReadSucceeded"
	leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: sdaTransitEvidenceCommands()[0], Output: "Processor board ID FOC12345678\n"}, {Command: sdaTransitEvidenceCommands()[1], Output: "Sessions for VRF default, total: 0, established: 0\n"}}
	status := leaf.DeepCopy().Status
	if err := api.Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	leaf.Status = status
	if err := api.Status().Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	outputs, err := read()
	if err != nil || len(outputs) != 2 {
		t.Fatal("fresh bound API result unavailable", err)
	}
	if err := verifyConfigurationChassisSerial("FOC12345678", outputs[0]); err != nil {
		t.Fatal(err)
	}
	leaf.Status.Outputs[1].Truncated = true
	if err := api.Status().Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	if data, err := read(); err == nil || data != nil {
		t.Fatal("truncated API evidence accepted")
	}
	leaf.Status.Outputs[1].Truncated = false
	if err := api.Status().Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	leaf.Spec.Operation.Commands[1] = "show clock"
	if err := api.Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	if leaf.Generation == generation {
		t.Fatal("spec edit did not advance API generation")
	}
	if data, err := read(); !errors.Is(err, errConfigurationStale) || data != nil {
		t.Fatal("changed API query accepted", err)
	}
	t.Log("fresh API UID/generation, one stable SDA request, separate simulated manager metadata and worker status, chassis identity, truncation and query-spec rejection verified; no semantic convergence or installed worker claim")
}
