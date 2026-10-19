//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	admission "k8s.io/api/admissionregistration/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Native HTTP is a controlled appliance; source CRDs, actor policy, original
// server UIDs, worker claim, receipt/refusal CAS and schema retention are real.
func TestEnvtestPnPMetadataPreparedSubmission(t *testing.T) {
	for _, mode := range []string{"verified", "late-sibling", "native-lost"} {
		t.Run(mode, func(t *testing.T) {
			qualifyEnvtestPnPMetadataSubmission(t, mode, false)
		})
	}
}

func TestEnvtestPnPTerminalRecovery(t *testing.T) {
	for _, mode := range []string{"terminal-source-missing", "terminal-invalid-intent", "terminal-config-gone", "terminal-missing-receipt", "terminal-foreign-owner"} {
		t.Run(mode, func(t *testing.T) { qualifyEnvtestPnPMetadataSubmission(t, mode, true) })
	}
}

func TestEnvtestPnPMetadataCancellation(t *testing.T) {
	for _, mode := range []string{"cancel-valid", "cancel-paused", "cancel-lost-ack", "cancel-conflict", "cancel-claim-race", "cancel-unknown", "cancel-source-gone", "cancel-worker-gone"} {
		t.Run(mode, func(t *testing.T) { qualifyEnvtestPnPMetadataSubmission(t, mode, false) })
	}
}

func TestEnvtestPnPMetadataEpochSettlement(t *testing.T) {
	for _, mode := range []string{"settle-edit-before", "settle-edit-marker", "settle-edit-delete", "settle-edit-release"} {
		t.Run(mode, func(t *testing.T) { qualifyEnvtestPnPMetadataSubmission(t, mode, false) })
	}
}

func TestEnvtestPnPRemovalPublicMetadataCycle(t *testing.T) {
	for _, mode := range []string{"verified", "late-sibling", "native-lost"} {
		t.Run(mode, func(t *testing.T) { qualifyEnvtestPnPMetadataSubmission(t, mode, true) })
	}
}

func qualifyEnvtestPnPMetadataSubmission(t *testing.T, mode string, public bool) {
	t.Helper()
	environment := &envtest.Environment{CRDDirectoryPaths: []string{"../../../config/crd/cisco.vk_networkcontrollers.yaml", "../../../config/crd/cisco.vk_ciscodevices.yaml", "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml", "../../../config/crd/ops.cisco.vk_controllerconfigurationreservations.yaml"}, ErrorIfCRDPathMissing: true}
	admin := qualificationAPIServerClientEnvironment(t, environment)
	caller := func(username, pod, uid string) ctrlclient.Client {
		cfg := rest.CopyConfig(environment.Config)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: []string{"system:masters"}}
		if uid != "" {
			cfg.Impersonate.Extra = map[string][]string{"authentication.kubernetes.io/pod-name": {pod}, "authentication.kubernetes.io/pod-uid": {uid}}
		}
		c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: admin.Scheme()})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	harness := &metadataSubmissionHarness{manager: caller("system:serviceaccount:system:cisco-virtual-kubelet-controller", "", ""), worker: func(t *testing.T, nc *cvk.NetworkController, pod *core.Pod) ctrlclient.Client {
		return caller("system:serviceaccount:"+nc.Namespace+":"+pod.Spec.ServiceAccountName, pod.Name, string(pod.UID))
	}, activate: activateMetadataSubmissionPolicies}
	install := installWirelessFeatureQualificationAPI(t, admin, "metadata-submission-"+mode)
	if public {
		exercisePnPRemovalPublicMetadataCycle(t, mode, install, harness)
	} else {
		exercisePnPMetadataPreparedSubmission(t, mode, install, harness)
	}
}

func activateMetadataSubmissionPolicies(t *testing.T, api ctrlclient.Client, bootstrapName, metadataName string, nc *cvk.NetworkController, serials []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	observation, err := contract.ReadMetadataScopeWorker(ctx, api, nc, "leases", serials, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	staged := nc.DeepCopy()
	staged.Status.Configuration.MetadataHandoff = &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: observation.BindingDigest, LeaseNamespace: "leases", WorkerPod: observation.Pod, WorkerUsername: observation.Username, PhysicalIdentities: observation.PhysicalIdentities, Operation: *nc.Status.Configuration.Operation.DeepCopy()}
	for _, name := range []string{bootstrapName, metadataName} {
		for {
			var err error
			if name == bootstrapName {
				err = api.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: "cvk-bootstrap-" + strings.Repeat("f", 32), Namespace: "leases"}}, ctrlclient.DryRunAll)
			} else {
				err = api.Status().Update(ctx, staged.DeepCopy(), ctrlclient.DryRunAll)
			}
			if (apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) && strings.Contains(err.Error(), name) {
				break
			}
			if err != nil || ctx.Err() != nil {
				t.Fatal("metadata submission admission not active", name, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		var policy admission.ValidatingAdmissionPolicy
		if err := api.Get(ctx, ctrlclient.ObjectKey{Name: name}, &policy); err != nil {
			t.Fatal(err)
		}
		policy.Status.ObservedGeneration = policy.Generation
		policy.Status.TypeChecking = &admission.TypeChecking{}
		if err := api.Status().Update(ctx, &policy); err != nil {
			t.Fatal(err)
		}
	}
}
