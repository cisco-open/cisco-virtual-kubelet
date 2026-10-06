// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestEnvtest_DeviceTLSCAAdmission(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{findControllerCRDPath(t)}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	}()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, ciskov1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "device-ca"}}); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "disabled", "insecure", "file", "empty-name", "invalid-name"} {
		t.Run(scenario, func(t *testing.T) {
			device := newDevice(scenario, "device-ca")
			device.Spec.TLS = &ciskov1.TLSConfig{Enabled: true, CASecretRef: &ciskov1.DeviceTLSCASecretReference{Name: "public-ca"}}
			switch scenario {
			case "disabled":
				device.Spec.TLS.Enabled = false
			case "insecure":
				device.Spec.TLS.InsecureSkipVerify = true
			case "file":
				device.Spec.TLS.CAFile = "/tmp/ca"
			case "empty-name":
				device.Spec.TLS.CASecretRef.Name = ""
			case "invalid-name":
				device.Spec.TLS.CASecretRef.Name = "../ca"
			}
			err := c.Create(ctx, device)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("unsafe TLS configuration admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var stored ciskov1.CiscoDevice
			if err := c.Get(ctx, client.ObjectKeyFromObject(device), &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Spec.TLS.CASecretRef == nil || stored.Spec.TLS.CASecretRef.Name != "public-ca" || stored.Spec.TLS.InsecureSkipVerify {
				t.Fatal("API dropped or changed verified CA reference")
			}
		})
	}
}
