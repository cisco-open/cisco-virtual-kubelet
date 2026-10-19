//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEnvtestDiscoveryCredentialHistoryCohort(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialCohortModes {
		t.Run(mode, func(t *testing.T) {
			a, c := journalFixture(t)
			installWirelessFeatureQualificationAPI(t, api, "credential-cohort-history-"+mode)(a, c)
			exerciseDiscoveryCredentialHistoryCohort(t, mode, func(state *cvk.NetworkControllerConfigurationStatus) *cvk.NetworkControllerConfigurationStatus {
				nc, err := a.currentController(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration = state
				if err := api.Status().Update(context.Background(), nc); err != nil {
					// Deliberate duplicate map keys are rejected by the real CRD before
					// reaching adapter validation; other corrupt-but-schema-valid cases
					// must persist and then be refused by the cohort validator.
					if mode != "duplicate-operation" || !apierrors.IsInvalid(err) {
						t.Fatal(err)
					}
					return state
				}
				var stored cvk.NetworkController
				if err := api.Get(context.Background(), ctrlclient.ObjectKeyFromObject(nc), &stored); err != nil {
					t.Fatal(err)
				}
				return stored.Status.Configuration
			})
		})
	}
}
