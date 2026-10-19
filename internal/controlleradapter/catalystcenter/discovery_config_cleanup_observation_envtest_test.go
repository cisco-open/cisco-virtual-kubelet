//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestEnvtestDiscoveryConfigCleanupObservation(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range discoveryCleanupObservationModes {
		t.Run(mode, func(t *testing.T) {
			install := installDiscoveryCleanupQualification(t, api, "discovery-cleanup-"+mode)
			exerciseDiscoveryCleanupObservation(t, mode, install)
		})
	}
}

func installDiscoveryCleanupQualification(t *testing.T, api ctrlclient.Client, name string) func(*adapter, *configapi.NetworkControllerConfig) {
	return func(a *adapter, c *configapi.NetworkControllerConfig) {
		ctx := context.Background()
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state := nc.Status.Configuration.DeepCopy()
		installWirelessFeatureQualificationAPI(t, api, name)(a, c)
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range state.Owners {
			state.Owners[i].ConfigUID = string(c.UID)
			state.Owners[i].ConfigName = c.Name
		}
		for i := range state.ActionApplications {
			state.ActionApplications[i].Operation.ConfigUID = string(c.UID)
			state.ActionApplications[i].Operation.ConfigName = c.Name
			state.ActionApplications[i].Operation.ControllerGeneration = nc.Generation
		}
		nc.Status.Configuration = state
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvtestDiscoveryConfigCleanupPreparation(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseDiscoveryCleanupPreparation(t, installDiscoveryCleanupQualification(t, api, "discovery-cleanup-preparation"))
}

func TestEnvtestDiscoveryConfigCleanupExecution(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range discoveryCleanupExecutionModes {
		t.Run(mode, func(t *testing.T) {
			exerciseDiscoveryCleanupExecution(t, mode, installDiscoveryCleanupQualification(t, api, "discovery-cleanup-exec-"+mode))
		})
	}
}

func TestEnvtestDiscoveryConfigDeletionDrain(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range discoveryDeletionDrainModes {
		t.Run(mode, func(t *testing.T) {
			exerciseDiscoveryDeletionDrain(t, mode, installDiscoveryCleanupQualification(t, api, "discovery-deletion-drain-"+mode))
		})
	}
}

func TestEnvtestDiscoveryConfigCleanupCompletion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range discoveryCompletionModes {
		t.Run(mode, func(t *testing.T) {
			exerciseDiscoveryCompletion(t, mode, installDiscoveryCleanupQualification(t, api, "discovery-completion-"+mode))
		})
	}
}

func TestEnvtestDiscoveryPublicConfigDeletion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range discoveryPublicDeletionModes {
		t.Run(mode, func(t *testing.T) {
			exerciseDiscoveryPublicDeletion(t, mode, installDiscoveryCleanupQualification(t, api, "discovery-public-"+mode))
		})
	}
}

func TestEnvtestDiscoveryScopePublicationJournalRace(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseDiscoveryScopePublicationRace(t, installDiscoveryCleanupQualification(t, api, "discovery-scope-race"))
}

func TestEnvtestDiscoveryOwnershipHandoff(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range discoveryHandoffModes {
		t.Run(mode, func(t *testing.T) {
			exerciseDiscoveryHandoff(t, mode, installDiscoveryCleanupQualification(t, api, "discovery-handoff-"+mode))
		})
	}
}
