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

// Real CRDs and API metadata; the native HTTP service remains a controlled
// fixture. Never loads kubeconfig or touches an installed cluster.
func TestEnvtestWirelessCleanAirBandUpdates(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseWirelessCleanAirManifestRestartUpdate(t, installWirelessFeatureQualificationAPI(t, api, "cleanair-bands"))
}

func TestEnvtestWirelessCleanAirRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"known", "missing-task", "create", "source", "foreign-owner"} {
		t.Run(mode, func(t *testing.T) {
			exerciseWirelessCleanAirManifestRestartUpdate(t, installWirelessFeatureQualificationAPI(t, api, "cleanair-recovery-"+mode), mode)
		})
	}
}

func installWirelessFeatureQualificationAPI(t *testing.T, api ctrlclient.Client, namespace string) func(*adapter, *configapi.NetworkControllerConfig) {
	t.Helper()
	return func(a *adapter, config *configapi.NetworkControllerConfig) {
		ctx := context.Background()
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
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
}

func TestEnvtestWirelessFRAFieldTransitions(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseWirelessFRAManifestRestartUpdate(t, installWirelessFeatureQualificationAPI(t, api, "fra-field-transitions"))
}

func TestEnvtestWirelessFeatureUpdateRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mixed := range []bool{false, true} {
		for _, mode := range []string{"known", "known-create", "missing-task", "create", "missing-create-task", "source", "foreign-owner"} {
			name := "fra-recovery-" + mode
			if mixed {
				name = "mixed-" + name
			}
			t.Run(name, func(t *testing.T) {
				exerciseWirelessFeatureRecovery(t, mixed, mode, installWirelessFeatureQualificationAPI(t, api, name))
			})
		}
	}
}

func TestEnvtestFeatureCreatorReceiptRelease(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"known", "missing", "duplicate", "pruned"} {
		t.Run(mode, func(t *testing.T) {
			exerciseFeatureCreatorRelease(t, mode, installWirelessFeatureQualificationAPI(t, api, "creator-release-"+mode))
		})
	}
}

func TestEnvtestFeatureAppliedReceiptRelease(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"known", "missing", "duplicate", "pruned", "creator-missing", "foreign-owner"} {
		t.Run(mode, func(t *testing.T) {
			exerciseFeatureAppliedRelease(t, mode, installWirelessFeatureQualificationAPI(t, api, "applied-release-"+mode))
		})
	}
}

func TestEnvtestWirelessFeaturePublicPrune(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"prune", "imports", "referenced", "native-drift", "missing-task", "tombstone-missing", "pruned", "cohort-capacity", "publish-owner", "publish-source"} {
		t.Run(mode, func(t *testing.T) {
			exerciseFeaturePublicPrune(t, mode, installWirelessFeatureQualificationAPI(t, api, "feature-prune-"+mode))
		})
	}
}

func TestEnvtestWirelessFeaturePublicRecreation(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"known", "retain-recreate", "occupied", "old-id-alias", "missing-tombstone", "capacity", "missing-task", "archive-pruned", "publish-owner", "publish-source"} {
		t.Run(mode, func(t *testing.T) {
			exerciseFeaturePublicRecreation(t, mode, installWirelessFeatureQualificationAPI(t, api, "feature-recreate-"+mode))
		})
	}
}

func TestEnvtestWirelessFeatureConfigDeletion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"known", "retain", "referenced", "missing-task", "tombstone-missing", "source-gone", "inflight-update", "inflight-create", "inflight-changed-source", "prepared-source-gone"} {
		t.Run(mode, func(t *testing.T) {
			exerciseFeatureConfigDeletion(t, mode, installWirelessFeatureQualificationAPI(t, api, "feature-delete-"+mode))
		})
	}
}

func TestEnvtestWirelessDefinitionRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for i, entry := range definitionRecoveryCases {
		for _, mode := range []string{"known", "taskless", "changed-source", "replaced-target", "child-failed", "child-pending", "child-foreign", "tree-unavailable"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) {
				exerciseWirelessDefinitionRecoveryPublic(t, entry, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("definition-recovery-%d-%s", i, mode)))
			})
		}
	}
}

func TestEnvtestWirelessDefinitionCreateRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for i, entry := range definitionRecoveryCases {
		for _, mode := range []string{"unknown-create", "create-taskless", "create-changed-source", "create-missing-target", "create-child-failed", "create-child-pending", "create-child-foreign", "create-tree-unavailable"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) {
				exerciseWirelessDefinitionRecoveryPublic(t, entry, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("create-recovery-%d-%s", i, mode)))
			})
		}
	}
}

func TestEnvtestWirelessNoDispatchReplan(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for i, entry := range definitionRecoveryCases {
		for _, mode := range []string{"field-edit", "read-error", "lost-ack"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) {
				exerciseWirelessNoDispatchReplan(t, entry, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("no-dispatch-%d-%s", i, mode)))
			})
		}
	}
}

func TestEnvtestWirelessFeatureNoDispatch(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"create", "import-update", "update", "prune", "recreate", "finalize"} {
		for _, mode := range []string{"selected", "companion", "lost-ack"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseWirelessFeatureNoDispatch(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "feature-no-dispatch-"+stage+"-"+mode))
			})
		}
	}
}
