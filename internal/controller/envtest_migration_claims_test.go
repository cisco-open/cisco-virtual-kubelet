// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package controller

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// Persist an outstanding install claim before interrupting policy deployment.
// Run the real reconciler twice with fresh instances against the real API:
// replacement/restart must fence future dispatch without erasing uncertainty.
// No worker or device transport is present; this qualifies manager persistence,
// not native RPC recovery or a live mixed-version rollout.
func TestEnvtest_MigrationRetainsOutstandingClaims(t *testing.T) {
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
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, ops.AddToScheme, ciskov1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, ns := range []string{"lab", "cvk-system"} {
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Dir(filepath.Dir(findControllerCRDPath(t)))
	data, err := os.ReadFile(filepath.Join(root, "examples/topology/iosxe-software-rollout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var example ops.IOSXESoftwareRollout
	if err := yaml.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing-policy", "replaced-policy", "cancel-missing-policy", "delete-missing-policy"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			target := policyFenceTarget("edge-"+scenario, "device-"+scenario, "leaf-"+scenario)
			r := policyFenceRollout([]ops.IOSXESoftwareRolloutPlannedTarget{target})
			r.Name, r.UID = scenario, ""
			r.Finalizers = []string{rolloutSafetyFinalizer}
			r.Spec.Plan = *example.Spec.Plan.DeepCopy()
			r.Spec.Control.RequestedBy = "migration-test"
			r.Spec.Control.RequestedAt = &metav1.Time{Time: now}
			ledger := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-system", Name: "ledger-" + scenario}}
			if err := c.Create(ctx, ledger); err != nil {
				t.Fatal(err)
			}
			r.Status.FrozenPlan.Policy.Name = "policy-" + scenario
			r.Status.FrozenPlan.Policy.LedgerName = ledger.Name
			r.Status.FrozenPlan.Policy.LedgerUID = string(ledger.UID)
			r.Status.EffectivePolicy.Policy = r.Status.FrozenPlan.Policy
			r.Status.FrozenPlan.CreatedAt = metav1.NewTime(now)
			r.Status.FrozenPlan.EncodedSizeBytes = 1
			r.Status.FrozenPlan.CampaignGeneration = 1
			status := r.Status
			r.Status = ops.IOSXESoftwareRolloutStatus{}
			if err := c.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			r.Status = status
			if err := c.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			leaf := policyFenceLeaf(r, target, "")
			leafStatus := leaf.Status
			leaf.Status = ops.IOSXESoftwareUpgradeStatus{}
			if err := c.Create(ctx, leaf); err != nil {
				t.Fatal(err)
			}
			leafStatus.ManagerAdmission.LeafUID = string(leaf.UID)
			leafStatus.ExecutionModel = ops.UpgradeExecutionModelAtMostOnceV1
			leafStatus.PrimarySupervisorInstallRequested = true
			leafStatus.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{
				Stage: ops.UpgradeManagedMutationPrimaryInstall, ReservationID: leafStatus.ManagerAdmission.ReservationID,
				PolicyEpoch: 1, ControlRevision: r.Spec.Control.Revision, ClaimedAt: metav1.NewTime(now),
			}}
			leaf.Status = leafStatus
			if err := c.Status().Update(ctx, leaf); err != nil {
				t.Fatal(err)
			}
			ledger.Data = policyFenceLedger(t, r, []ops.IOSXESoftwareRolloutPlannedTarget{target},
				map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationGranted).Data
			if err := c.Update(ctx, ledger); err != nil {
				t.Fatal(err)
			}
			if scenario == "replaced-policy" {
				policy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-system", Name: r.Status.FrozenPlan.Policy.Name}, Data: map[string]string{topologyrollout.PolicyDataKey: "partial-deployment"}}
				if err := c.Create(ctx, policy); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "delete-missing-policy" {
				if err := c.Delete(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "cancel-missing-policy" {
				r.Spec.Control.Cancel = true
				r.Spec.Control.Revision++
				if err := c.Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			for restart := 0; restart < 2; restart++ {
				reconciler := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: c, Scheme: scheme,
					TopologyPolicyNamespace: "cvk-system", TopologyPolicyName: r.Status.FrozenPlan.Policy.Name}
				_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(r)})
				if scenario == "delete-missing-policy" {
					if err == nil || !strings.Contains(err.Error(), "deletion safety policy unavailable") {
						t.Fatalf("expected exact deletion safety hold, got %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				var stored ops.IOSXESoftwareUpgrade
				if err := c.Get(ctx, client.ObjectKeyFromObject(leaf), &stored); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(stored.Status.ManagedMutationClaims, leaf.Status.ManagedMutationClaims) ||
					stored.Status.ManagerAdmission.State != ops.UpgradeManagerAdmissionRevoked ||
					stored.Status.CompletionTime != nil || stored.Status.PrimarySupervisorInstalled {
					t.Fatalf("restart %d lost claim or admitted/settled outstanding operation: %+v", restart, stored.Status)
				}
				store := topologyrollout.Store{Client: c, APIReader: c, Key: client.ObjectKeyFromObject(ledger), ExpectedUID: ledger.UID}
				_, retained, err := store.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				reservation, found := retained.Reservations[leaf.Status.ManagerAdmission.ReservationID]
				if !found || reservation.ChildUID != string(leaf.UID) || len(retained.Reservations) != 1 {
					t.Fatalf("restart %d lost or replaced outstanding reservation: %+v", restart, retained)
				}
				var retainedCampaign ops.IOSXESoftwareRollout
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), &retainedCampaign); err != nil {
					t.Fatal(err)
				}
				if !hasExactString(retainedCampaign.Finalizers, rolloutSafetyFinalizer) {
					t.Fatal("unresolved campaign lost its safety finalizer")
				}
			}
			t.Log("claim and exact reservation retained across interruption and reconciler replacement; future admission revoked")
		})
	}
}
