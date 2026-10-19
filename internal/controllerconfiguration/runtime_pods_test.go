// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestRuntimeInstallationPodBinding(t *testing.T) {
	for _, protocol := range []string{Protocol, GroupReservationProtocol} {
		t.Run(protocol, func(t *testing.T) { testRuntimeInstallationPodBinding(t, protocol) })
	}
}
func testRuntimeInstallationPodBinding(t *testing.T, protocol string) {
	binding := RuntimeAdmissionBinding{Protocol: protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network"}
	encoded, err := binding.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"current", "old device", "old connector", "replaced connector", "different installation", "mixed device protocol", "mixed connector protocol", "valueFrom", "duplicate", "sidecar only", "wrong account", "mismatched protocol", "missing protocol", "unknown protocol"} {
		t.Run(scenario, func(t *testing.T) {
			mkpod := func(name, container string) *core.Pod {
				return &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab", UID: types.UID(name + "-uid")}, Spec: core.PodSpec{Containers: []core.Container{{Name: container, Env: []core.EnvVar{{Name: RuntimeAdmissionEnvironment, Value: encoded}}}}}}
			}
			device, connector := mkpod("device", "cisco-vk"), mkpod("connector", "controller-worker")
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: protocol, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: device.Name, UID: string(device.UID)}, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: connector.Name, UID: string(connector.UID)}, DeviceWorkerUsername: "system:serviceaccount:lab:network"}}
			switch scenario {
			case "mismatched protocol":
				r.Spec.Protocol = GroupReservationProtocol
				if protocol == GroupReservationProtocol {
					r.Spec.Protocol = Protocol
				}
			case "missing protocol":
				r.Spec.Protocol = ""
			case "unknown protocol":
				r.Spec.Protocol = "future"
			case "old device":
				device.Spec.Containers[0].Env = nil
			case "old connector":
				connector.Spec.Containers[0].Env = nil
			case "replaced connector":
				connector.UID = "replacement"
			case "mixed device protocol", "mixed connector protocol":
				b := binding
				b.Protocol = GroupReservationProtocol
				if protocol == GroupReservationProtocol {
					b.Protocol = Protocol
				}
				target := connector
				if scenario == "mixed device protocol" {
					target = device
				}
				target.Spec.Containers[0].Env[0].Value, _ = b.Encode()
			case "different installation":
				b := binding
				b.ManagerUsername = "system:serviceaccount:system:other"
				connector.Spec.Containers[0].Env[0].Value, _ = b.Encode()
			case "valueFrom":
				connector.Spec.Containers[0].Env[0].ValueFrom = &core.EnvVarSource{FieldRef: &core.ObjectFieldSelector{FieldPath: "metadata.name"}}
			case "duplicate":
				connector.Spec.Containers[0].Env = append(connector.Spec.Containers[0].Env, connector.Spec.Containers[0].Env[0])
			case "sidecar only":
				connector.Spec.Containers[0].Name = "cisco-vk"
			case "wrong account":
				r.Spec.DeviceWorkerUsername = "system:serviceaccount:lab:other"
			}
			scheme := runtime.NewScheme()
			_ = core.AddToScheme(scheme)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, connector).Build()
			err := ReadRuntimeInstallationBinding(context.Background(), c, r, binding)
			if (err == nil) != (scenario == "current") {
				t.Fatalf("scenario=%s err=%v", scenario, err)
			}
		})
	}
}
