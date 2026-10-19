// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var lifecycleTestOnce sync.Once

const lifecycleTestFinalizer = "tests.cisco.vk/config-cleanup"

func registerLifecycleTestAdapter() {
	lifecycleTestOnce.Do(func() {
		controlleradapter.Register(controlleradapter.Registration{
			Descriptor: controlleradapter.Descriptor{Type: "lifecycle-test", DisplayName: "Lifecycle test", NetAsCode: cvk.NetworkControllerNetAsCodeStatus{Format: "netascode-lifecycle-test", Stripe: "lifecycle_test", ModelVersions: []string{"1.0.0"}, Sections: []string{"inventory"}}, Capabilities: []string{"config"}, WorkerClusterRole: controlleradapter.DefaultWorkerClusterRole},
			Factory: func(controlleradapter.Options) (controlleradapter.Adapter, error) {
				panic("manager constructed a product adapter")
			},
			ConfigurationLifecycle: &controlleradapter.ConfigurationLifecycle{Finalizer: lifecycleTestFinalizer, Eligible: func(nc *cvk.NetworkController, c *config.NetworkControllerConfig) bool {
				return c.Spec.DeletionPolicy == config.NetworkControllerRetentionPolicyDelete && c.Labels["family"] != "additional"
			}, Completed: func(nc *cvk.NetworkController, c *config.NetworkControllerConfig) bool {
				return len(c.Status.Conditions) == 1 && c.Status.Conditions[0].Status == metav1.ConditionTrue && c.Status.Conditions[0].ObservedGeneration == c.Generation && nc.Status.Configuration == nil
			}},
			ConfigurationLifecycles: []controlleradapter.ConfigurationLifecycle{{Finalizer: "tests.cisco.vk/additional-cleanup", Eligible: func(nc *cvk.NetworkController, c *config.NetworkControllerConfig) bool {
				return c.Labels["family"] == "additional" && c.Spec.DeletionPolicy == config.NetworkControllerRetentionPolicyDelete
			}, Completed: func(nc *cvk.NetworkController, c *config.NetworkControllerConfig) bool {
				return len(c.Status.Conditions) == 1 && c.Status.Conditions[0].Status == metav1.ConditionTrue && c.Status.Conditions[0].ObservedGeneration == c.Generation && nc.Status.Configuration == nil
			}}},
		})
	})
}

type lifecyclePatchAudit struct {
	client.Client
	patches int
}

func (c *lifecyclePatchAudit) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	data, err := patch.Data(obj)
	if err != nil {
		return err
	}
	var body map[string]any
	if json.Unmarshal(data, &body) != nil {
		return errConfigurationPatch
	}
	if len(body) != 1 || body["metadata"] == nil {
		return errConfigurationPatch
	}
	c.patches++
	return c.Client.Patch(ctx, obj, patch, opts...)
}

var errConfigurationPatch = apierrors.NewBadRequest("lifecycle patch changed fields outside metadata")

func TestNetworkControllerConfigLifecycleManager(t *testing.T) {
	registerLifecycleTestAdapter()
	for _, mode := range []string{"complete", "pending", "generation", "paused", "foreign-finalizer", "additional", "additional-generation", "family-conflict"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := config.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := core.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "controller", Namespace: "lab", UID: "controller", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: "lifecycle-test"}}
			cfg := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "intent", Namespace: "lab", UID: "intent", Generation: 1, Finalizers: []string{"example.com/other"}}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: nc.Name}, DeletionPolicy: config.NetworkControllerRetentionPolicyDelete}}
			selectedFinalizer := lifecycleTestFinalizer
			if strings.HasPrefix(mode, "additional") {
				cfg.Labels = map[string]string{"family": "additional"}
				selectedFinalizer = "tests.cisco.vk/additional-cleanup"
			}
			if mode == "family-conflict" {
				cfg.Finalizers = append(cfg.Finalizers, lifecycleTestFinalizer, "tests.cisco.vk/additional-cleanup")
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, cfg).WithIndex(&config.NetworkControllerConfig{}, networkControllerRefIndex, networkControllerConfigRefIndexValues).WithObjects(nc, cfg).Build()
			audit := &lifecyclePatchAudit{Client: kube}
			r := &NetworkControllerReconciler{Client: audit, APIReader: kube}
			if mode == "reader-missing" {
				r.APIReader = nil
				if changed, _, err := r.reconcileConfigLifecycles(ctx, nc); err == nil || changed || audit.patches != 0 {
					t.Fatal("cached-only lifecycle was allowed", changed, err)
				}
				return
			}
			if mode == "family-conflict" {
				if err := kube.Delete(ctx, cfg); err != nil {
					t.Fatal(err)
				}
				changed, pending, err := r.reconcileConfigLifecycles(ctx, nc)
				if err != nil || changed || !pending || audit.patches != 0 {
					t.Fatal("ambiguous family changed finalizers", changed, pending, err)
				}
				return
			}
			original := cfg.Spec
			changed, pending, err := r.reconcileConfigLifecycles(ctx, nc)
			if err != nil || !changed || pending {
				t.Fatal("manager enrollment failed", changed, pending, err)
			}
			if err = kube.Get(ctx, client.ObjectKeyFromObject(cfg), cfg); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(cfg.Finalizers, selectedFinalizer) || !reflect.DeepEqual(cfg.Spec, original) {
				t.Fatal("enrollment changed intent")
			}
			if err = kube.Delete(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if err = kube.Get(ctx, client.ObjectKeyFromObject(cfg), cfg); err != nil {
				t.Fatal(err)
			}
			cfg.Status.Conditions = []metav1.Condition{{Type: "Complete", Status: metav1.ConditionTrue, Reason: "Verified", Message: "proof", ObservedGeneration: cfg.Generation, LastTransitionTime: metav1.Now()}}
			if mode == "pending" {
				cfg.Status.Conditions[0].Status = metav1.ConditionFalse
			}
			if mode == "generation" || mode == "additional-generation" {
				cfg.Status.Conditions[0].ObservedGeneration++
			}
			if err = kube.Status().Update(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if mode == "paused" {
				nc.Spec.Paused = true
				if err = kube.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			changed, pending, err = r.reconcileConfigLifecycles(ctx, nc)
			if err != nil || !pending {
				t.Fatal("manager finalization failed", changed, pending, err)
			}
			if err = kube.Get(ctx, client.ObjectKeyFromObject(cfg), cfg); err != nil {
				t.Fatal(err)
			}
			expected := mode == "complete" || mode == "foreign-finalizer" || mode == "additional"
			if changed != expected || slices.Contains(cfg.Finalizers, selectedFinalizer) == expected || !slices.Contains(cfg.Finalizers, "example.com/other") || !reflect.DeepEqual(cfg.Spec, original) {
				t.Fatal("unsafe finalizer mutation", mode, changed, cfg.Finalizers)
			}
			wantPatches := 1
			if expected {
				wantPatches = 2
			}
			if audit.patches != wantPatches {
				t.Fatal("unexpected metadata patches", audit.patches)
			}
		})
	}
}
