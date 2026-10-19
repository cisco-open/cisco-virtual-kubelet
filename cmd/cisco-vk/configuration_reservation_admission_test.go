// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"context"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigurationManagerRefusesUnknownProtocol(t *testing.T) {
	reader := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	authorize := configurationReservationManagerAuthorizer(&rest.Config{}, reader, types.NamespacedName{Namespace: "admin", Name: "policy"}, "apps", "network", "leases")
	for _, protocol := range []string{"", "unknown", contract.Protocol + "," + contract.GroupReservationProtocol} {
		row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "edge"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: protocol, DeviceWorkerUsername: "system:serviceaccount:edge:network"}}
		err := authorize(context.Background(), row)
		if err == nil || !strings.Contains(err.Error(), "manager authority is incomplete") {
			t.Fatalf("unsupported protocol reached installation discovery: %v", err)
		}
	}
}

// Recognizing either protocol must still require the administrator's installed
// policy; group reservations cannot skip installation discovery.
func TestConfigurationManagerRequiresInstalledPolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := core.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	authorize := configurationReservationManagerAuthorizer(&rest.Config{}, reader, types.NamespacedName{Namespace: "admin", Name: "policy"}, "apps", "network", "leases")
	for _, protocol := range []string{contract.Protocol, contract.GroupReservationProtocol} {
		t.Run(protocol, func(t *testing.T) {
			row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "edge"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: protocol, DeviceWorkerUsername: "system:serviceaccount:edge:network"}}
			if err := authorize(context.Background(), row); !apierrors.IsNotFound(err) {
				t.Fatalf("missing installation policy must deny authorization: %v", err)
			}
			row.Spec.DeviceWorkerUsername = "system:serviceaccount:edge:apps"
			if err := authorize(context.Background(), row); err == nil || !strings.Contains(err.Error(), "manager authority is incomplete") {
				t.Fatalf("wrong worker identity reached installation discovery: %v", err)
			}
		})
	}
}

func TestConfigurationConnectorDescriptor(t *testing.T) {
	d, ok := controlleradapter.DescriptorFor("catalyst-center")
	if !ok {
		t.Fatal("missing Catalyst Center registration")
	}
	digestArg := "--controller-descriptor-digest=" + controlleradapter.DescriptorDigest(d)
	for _, tc := range []struct {
		name   string
		args   []string
		change func(*cvk.NetworkController, *core.Pod)
		want   bool
	}{
		{"current", []string{"controller-worker", digestArg}, nil, true},
		{"missing", []string{"controller-worker"}, nil, false},
		{"old contract", []string{"controller-worker", "--controller-descriptor-digest=old"}, nil, false},
		{"duplicate", []string{"controller-worker", digestArg, digestArg}, nil, false},
		{"split override", []string{"controller-worker", digestArg, "--controller-descriptor-digest", "old"}, nil, false},
		{"new controller generation", []string{"controller-worker", digestArg}, func(n *cvk.NetworkController, _ *core.Pod) { n.Generation++ }, false},
		{"replacement pod", []string{"controller-worker", digestArg}, func(_ *cvk.NetworkController, p *core.Pod) { p.UID = "new-pod" }, false},
		{"other adapter", []string{"controller-worker", digestArg}, func(n *cvk.NetworkController, _ *core.Pod) { n.Spec.Type = "nexus-dashboard" }, false},
		{"wrong container", []string{"controller-worker", digestArg}, func(_ *cvk.NetworkController, p *core.Pod) { p.Spec.Containers[0].Name = "sidecar" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = core.AddToScheme(scheme)
			_ = cvk.AddToScheme(scheme)
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: "lab", UID: "controller", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center"}}
			p := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "lab", UID: "pod"}, Spec: core.PodSpec{Containers: []core.Container{{Name: "controller-worker", Args: tc.args}}}}
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab"}, Spec: ops.ControllerConfigurationReservationSpec{Controller: ops.ConfigurationReservationReference{Name: "cc", UID: "controller"}, ControllerGeneration: 1, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "pod"}}}
			if tc.change != nil {
				tc.change(nc, p)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc, p).Build()
			if err := verifyConfigurationConnectorDescriptor(context.Background(), reader, r); (err == nil) != tc.want {
				t.Fatalf("err=%v want success=%v", err, tc.want)
			}
		})
	}
}
