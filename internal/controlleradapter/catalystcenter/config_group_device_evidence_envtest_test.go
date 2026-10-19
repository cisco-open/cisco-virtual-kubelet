// Copyright © 2026 Cisco Systems Inc.
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
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actual private API metadata/status separation; manager/worker writes below
// are explicitly simulated. No kubeconfig, installed cluster or device is used.
func TestEnvtestConfigurationDeviceEvidence(t *testing.T) {
	exerciseConfigurationDeviceEvidenceAPI(t, false)
}

func TestEnvtestConfigurationScalarDeviceEvidence(t *testing.T) {
	exerciseConfigurationDeviceEvidenceAPI(t, true)
}

func TestEnvtestConfigurationFabricDeviceEvidence(t *testing.T) {
	exerciseConfigurationDeviceEvidenceAPI(t, false, true)
}

func exerciseConfigurationDeviceEvidenceAPI(t *testing.T, scalars bool, fabricMode ...bool) {
	t.Helper()
	api := qualificationAPIServerClient(t, "../../../config/crd/ops.cisco.vk_deviceoperations.yaml")
	if err := ops.AddToScheme(api.Scheme()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	namespace := "bulk-device-evidence"
	prefix, annotation := "cc-bulk-evidence-", wiredBulkEvidenceBindingAnnotation
	pending := errWiredBulkDeviceEvidencePending
	fabric := len(fabricMode) == 1 && fabricMode[0]
	if fabric {
		namespace = "fabric-device-evidence"
		prefix = "cc-fabric-evidence-"
		annotation = fabricDevicesBulkEvidenceAnnotation
		pending = errFabricDevicesBulkEvidencePending
	}
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	a := &adapter{statusReader: api, statusWriter: api}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "cc", UID: "controller-uid"}}
	op := &cvk.NetworkControllerConfigurationOperation{ID: "operation", TaskID: "task", PhysicalGroup: &cvk.NetworkControllerConfigurationGroupIdentity{Digest: "group"}}
	member := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}, Spec: ops.ControllerConfigurationReservationSpec{Device: ops.ConfigurationReservationReference{Name: "edge", UID: "device-uid"}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "worker-uid"}, DeviceWorkerUsername: "system:serviceaccount:" + namespace + ":worker"}}
	completed := time.Now().UTC().Add(-5 * time.Second).Truncate(time.Second)
	commands := []string{"show version", "show ntp associations"}
	if fabric {
		commands[1] = "show lisp session all"
	}
	var settings []networkSetting
	if scalars {
		settings = []networkSetting{{kind: "banner", bannerType: "Custom", banner: "\nAuthorized access\n"}, {kind: "timezone", timezone: "Etc/UTC"}}
		var err error
		commands, err = wiredBulkNetworkCommands(settings)
		if err != nil {
			t.Fatal(err)
		}
	}
	expected := configurationDeviceProofOperation(nc, op, member, "result", completed, commands, prefix, annotation)
	read := func() ([]string, error) {
		return a.readConfigurationDeviceProof(ctx, expected, member, annotation, completed, time.Now().UTC(), pending)
	}
	if _, err := read(); !errors.Is(err, pending) {
		t.Fatal("first worker read did not remain pending", err)
	}
	var leaf ops.DeviceOperation
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(expected), &leaf); err != nil {
		t.Fatal(err)
	}
	if leaf.UID == "" || leaf.Generation != 1 || !leaf.CreationTimestamp.After(completed) || managedprotocol.NetworkObjectBindingComplete(leaf.Annotations) {
		t.Fatal("API identity or unbound request changed")
	}
	uid, generation, created := leaf.UID, leaf.Generation, leaf.CreationTimestamp
	for range 2 {
		if _, err := read(); !errors.Is(err, pending) {
			t.Fatal("unbound read escaped", err)
		}
	}
	var list ops.DeviceOperationList
	if err := api.List(ctx, &list, ctrlclient.InNamespace(namespace)); err != nil || len(list.Items) != 1 || list.Items[0].UID != uid {
		t.Fatal("pending evidence duplicated", err)
	}
	// Supply a simulated manager binding and worker result through separate API
	// writes. Production callers cannot stamp these via the connector bridge.
	now := time.Now().UTC()
	stampLANProofEnvelope(&leaf, member, created.Time, created.Time, now)
	leaf.Status.Conditions[0].Reason = "FixtureReadSucceeded"
	leaf.Status.Conditions[0].Message = "Simulated worker completed the read-only request"
	leaf.UID, leaf.Generation, leaf.CreationTimestamp = uid, generation, created
	leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: "show version", Output: "Processor board ID SERIAL\n"}, {Command: commands[1], Output: "fixture association"}}
	if scalars {
		leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: commands[0], Output: "Processor board ID SERIAL\n"}, {Command: commands[1], Output: "banner motd ^C\nAuthorized access\n^C\n"}, {Command: commands[2], Output: "clock timezone UTC 0\n"}}
	}
	status := leaf.DeepCopy().Status
	if err := api.Update(ctx, &leaf); err != nil {
		t.Fatal(err)
	}
	leaf.Status = status
	if err := api.Status().Update(ctx, &leaf); err != nil {
		t.Fatal(err)
	}
	outputs, err := read()
	if err != nil || len(outputs) != len(commands) {
		t.Fatal("fresh API worker result unavailable", err)
	}
	if scalars {
		if err := verifyWiredBulkNetworkCLI(settings, outputs); err != nil {
			t.Fatal("API status changed scalar observation semantics", err)
		}
	}
	leaf.Status.Outputs[1].Redacted = true
	if err := api.Status().Update(ctx, &leaf); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); !errors.Is(err, errInvalidResponse) {
		t.Fatal("redacted API result accepted", err)
	}
	leaf.Spec.Operation.Commands[1] = "show clock"
	if err := api.Update(ctx, &leaf); err != nil {
		t.Fatal(err)
	}
	if leaf.Generation != generation+1 {
		t.Fatal("API did not advance spec generation")
	}
	if _, err := read(); !errors.Is(err, errConfigurationStale) {
		t.Fatal("changed query plan accepted", err)
	}
	t.Log("private API retained one pending request, assigned real UID/timestamps/generation, separated worker status, and refused redaction/spec replacement; actor writes simulated")
}
