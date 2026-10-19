// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"fmt"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEnvtestDiscoveryMixedOwnership(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for i, mode := range []discoveryCycleScenario{{combined: true, replacement: true}, {combined: true, replacement: true, reverseSections: true}, {combined: true, replacement: true, inventoryOnlyTags: true}, {combined: true, replacement: true, composeLater: true}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			mode.install = func(a *adapter, config *configapi.NetworkControllerConfig) {
				ctx := context.Background()
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				namespace := fmt.Sprint("mixed-discovery-", i)
				if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
					t.Fatal(err)
				}
				nc.ObjectMeta = metav1.ObjectMeta{Name: nc.Name, Namespace: namespace}
				nc.Status = cvk.NetworkControllerStatus{}
				if err := api.Create(ctx, nc); err != nil {
					t.Fatal(err)
				}
				config.ObjectMeta = metav1.ObjectMeta{Name: config.Name, Namespace: namespace}
				config.Status = configapi.NetworkControllerConfigStatus{}
				if err := api.Create(ctx, config); err != nil {
					t.Fatal(err)
				}
				a.statusReader, a.statusWriter = api, api
				a.key = ctrlclient.ObjectKeyFromObject(nc)
				a.uid, a.generation = nc.UID, nc.Generation
			}
			discoveryManifestScenario(t, mode)
		})
	}
}
