// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"testing"
)

// Private ephemeral API server only; never uses the user's kubeconfig/cluster.
func qualificationAPIServerClient(t *testing.T, extraCRDs ...string) ctrlclient.Client {
	t.Helper()
	paths := append([]string{"../../../config/crd/cisco.vk_networkcontrollers.yaml"}, extraCRDs...)
	environment := &envtest.Environment{CRDDirectoryPaths: paths, ErrorIfCRDPathMissing: true}
	return qualificationAPIServerClientEnvironment(t, environment)
}

func qualificationAPIServerClientEnvironment(t *testing.T, environment *envtest.Environment) ctrlclient.Client {
	t.Helper()
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	if err := core.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	// Manager lifecycle qualification reads the existing worker Deployment and
	// RBAC bootstrap before reaching adapter finalizer callbacks.
	if err := apps.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbac.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cvk.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := configapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	api, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return api
}
