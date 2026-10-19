// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func reportFixture(t *testing.T) (*configReconciler, *configapi.NetworkControllerConfig, *ciskov1.NetworkController) {
	return reportFixtureEndpoint(t, "https://catc.example")
}

func reportFixtureEndpoint(t *testing.T, endpoint string) (*configReconciler, *configapi.NetworkControllerConfig, *ciskov1.NetworkController) {
	t.Helper()
	nc := &ciskov1.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "catc", UID: "controller", Generation: 1}, Spec: ciskov1.NetworkControllerSpec{Type: TypeName, Endpoint: endpoint, CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "credentials"}}}
	config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "sites", UID: "config", Generation: 1}, Spec: configapi.NetworkControllerConfigSpec{
		ControllerRef: configapi.NetworkControllerRef{Name: "catc"}, ManagedSections: []string{"sites"},
		ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true},
		Source:      configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"sites":{"areas":[{"name":"Lab","parent_name":"Global"}]}}}`)}},
	}}
	scheme := runtime.NewScheme()
	_ = ciskov1.AddToScheme(scheme)
	_ = configapi.AddToScheme(scheme)
	_ = ops.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(config, nc, &ops.ControllerConfigurationReservation{}).WithIndex(&corev1.Pod{}, "spec.nodeName", func(obj ctrlclient.Object) []string { return []string{obj.(*corev1.Pod).Spec.NodeName} }).WithObjects(config, nc).Build()
	a := &adapter{key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation, statusReader: k8s, statusWriter: k8s}
	return &configReconciler{a: a}, config, nc
}
func TestConfigReportReadOnlyAndFailClosed(t *testing.T) {
	for _, mode := range []string{"in-sync", "drift", "apply", "unknown-section", "delete", "unresolved", "missing-field", "source-changed", "controller-paused", "api-failure"} {
		t.Run(mode, func(t *testing.T) {
			r, config, nc := reportFixture(t)
			ctx := context.Background()
			reads := 0
			_ = r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config)
			switch mode {
			case "apply":
				config.Spec.Mode = configapi.NetworkControllerApplyModeApply
				config.Spec.ManagedSections = []string{"wireless"} // execution not implemented for this section
			case "unknown-section":
				config.Spec.ManagedSections = []string{"wireless"}
			case "delete":
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "unresolved":
				config.Spec.ModelSource.Resolved = false
			case "missing-field":
				config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"sites":{"buildings":[{"name":"Building","parent_name":"Global/Lab","address":"secret"}]}}}`)
			}
			if err := r.a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			r.a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if req.Method != "GET" || req.URL.Path != sitesPath {
					t.Errorf("mutation or unexpected API: %s %s", req.Method, req.URL.Path)
				}
				if mode == "source-changed" {
					var latest configapi.NetworkControllerConfig
					_ = r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &latest)
					latest.Generation++
					if err := r.a.statusWriter.Update(ctx, &latest); err != nil {
						t.Error(err)
					}
				}
				if mode == "controller-paused" {
					_ = r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), nc)
					nc.Spec.Paused = true
					if err := r.a.statusWriter.Update(ctx, nc); err != nil {
						t.Error(err)
					}
				}
				if mode == "api-failure" {
					w.WriteHeader(500)
					fmt.Fprint(w, "secret")
					return
				}
				sites := sampleSites()
				if mode == "drift" {
					sites = sites[:1]
				}
				json.NewEncoder(w).Encode(map[string]any{"response": sites})
			})
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "lab", Name: "sites"}})
			if err != nil {
				t.Fatal(err)
			}
			var got configapi.NetworkControllerConfig
			_ = r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &got)
			switch mode {
			case "in-sync":
				if got.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
					t.Fatalf("%+v", got.Status)
				}
			case "drift":
				if got.Status.Phase != configapi.NetworkControllerConfigPhaseDrifted || len(got.Status.Drift) != 1 {
					t.Fatalf("%+v", got.Status)
				}
			case "source-changed", "controller-paused":
				if got.Status.ObservedGeneration != 0 {
					t.Fatal("published stale observation")
				}
			default:
				if got.Status.Phase != configapi.NetworkControllerConfigPhaseFailed {
					t.Fatalf("unsupported operation accepted: %+v", got.Status)
				}
			}
			if (mode == "apply" || mode == "unknown-section" || mode == "delete" || mode == "unresolved") && reads != 0 {
				t.Fatal("unsupported intent contacted controller")
			}
			if got.Status.LastAppliedHash != "" || got.Status.LastAppliedTime != nil {
				t.Fatal("report claimed apply")
			}
		})
	}
}

func TestConfigReportConfigMapRevisionAndWatch(t *testing.T) {
	ctx := context.Background()
	r, config, _ := reportFixture(t)
	if err := r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "intent", UID: "source"}, Data: map[string]string{"model.yaml": string(config.Spec.Source.Inline.Raw)}}
	if err := r.a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: "intent", Key: "model.yaml"}}
	if err := r.a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	requests := r.a.configsForSource(ctx, cm)
	if len(requests) != 1 || requests[0].Name != "sites" {
		t.Fatal("source watch did not find dependent intent")
	}
	other := cm.DeepCopy()
	other.Namespace = "other"
	if len(r.a.configsForSource(ctx, other)) != 0 {
		t.Fatal("cross-namespace source event accepted")
	}
	r.a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if err := r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
			t.Error(err)
		}
		cm.Data["model.yaml"] = `{"catalyst_center":{"sites":{"areas":[]}}}`
		if err := r.a.statusWriter.Update(ctx, cm); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
	})
	result, err := r.Reconcile(ctx, requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("source change did not cause a new observation")
	}
	var got configapi.NetworkControllerConfig
	if err = r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ObservedGeneration != 0 {
		t.Fatal("published observation for stale ConfigMap")
	}
}
