// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Full public reconciles use actual persisted Config/Controller objects and
// API-generated generations/resourceVersions. Native CC APIs are HTTP fixtures.
func TestEnvtestDiscoveryActivePublicReconciliation(t *testing.T) {
	a, config, world := envtestDiscoveryFixture(t)
	exerciseDiscoveryActiveLifecycle(t, a, config, world)
	if config.Generation < 3 || config.Status.ObservedGeneration != config.Generation {
		t.Fatal("real API generation lifecycle not observed")
	}
}
func TestEnvtestDiscoveryCohortPublicReconciliation(t *testing.T) {
	a, config, world := envtestDiscoveryFixture(t)
	exerciseDiscoveryCohortProgress(t, a, config, world)
}
func envtestDiscoveryFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *discoveryPruneWorld) {
	t.Helper()
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	ctx := context.Background()
	a, config, world := discoveryPruneFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nc.Namespace}}); err != nil {
		t.Fatal(err)
	}
	state := nc.Status.Configuration.DeepCopy()
	nc.ObjectMeta = metav1.ObjectMeta{Name: nc.Name, Namespace: nc.Namespace}
	nc.Status = cvk.NetworkControllerStatus{}
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.ObjectMeta = metav1.ObjectMeta{Name: config.Name, Namespace: config.Namespace}
	config.Status.Conditions = nil
	config.Status.Phase = ""
	if err := api.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = state
	for i := range state.Owners {
		state.Owners[i].ConfigName = config.Name
		state.Owners[i].ConfigUID = string(config.UID)
	}
	for i := range state.ActionApplications {
		state.ActionApplications[i].Operation.ConfigName = config.Name
		state.ActionApplications[i].Operation.ConfigUID = string(config.UID)
		state.ActionApplications[i].Operation.ControllerGeneration = nc.Generation
	}
	if err := api.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.statusReader, a.statusWriter = api, api
	a.key = ctrlclient.ObjectKeyFromObject(nc)
	a.uid, a.generation = nc.UID, nc.Generation
	return a, config, world
}
