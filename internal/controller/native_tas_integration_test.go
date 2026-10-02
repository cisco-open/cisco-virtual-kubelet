// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build native_tas_integration

package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestNativeTASServedSchedulingGroupGuard is invoked only by the disposable
// Kubernetes 1.37 kind lane. It closes the compatibility boundary that a fake
// client cannot cover: the apiserver must persist spec.schedulingGroup and the
// CVK binary, whose typed core/v1 Pod predates that field, must still discover
// it through its uncached unstructured guard before an eviction is attempted.
func TestNativeTASServedSchedulingGroupGuard(t *testing.T) {
	expectedContext := os.Getenv("CVK_NATIVE_TAS_TEST_CONTEXT")
	namespace := os.Getenv("CVK_NATIVE_TAS_GUARD_NAMESPACE")
	podName := os.Getenv("CVK_NATIVE_TAS_GUARD_POD")
	if expectedContext == "" || namespace == "" || podName == "" {
		t.Fatal("native TAS integration context, namespace and Pod are required")
	}
	if !strings.HasPrefix(expectedContext, "kind-") {
		t.Fatalf("refusing non-kind context %q", expectedContext)
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	rawConfig, err := loadingRules.Load()
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	if rawConfig.CurrentContext != expectedContext {
		t.Fatalf("current context = %q, want disposable %q", rawConfig.CurrentContext, expectedContext)
	}
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{CurrentContext: expectedContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("load REST config: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	raw := &unstructured.Unstructured{}
	raw.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	key := client.ObjectKey{Namespace: namespace, Name: podName}
	if err := apiClient.Get(ctx, key, raw); err != nil {
		t.Fatalf("read served Pod: %v", err)
	}
	if _, found, err := unstructured.NestedFieldNoCopy(raw.Object, "spec", "schedulingGroup"); err != nil || !found {
		t.Fatalf("served Pod has no readable spec.schedulingGroup: found=%v err=%v", found, err)
	}

	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: raw.GetNamespace(), Name: raw.GetName(),
		UID: raw.GetUID(), ResourceVersion: raw.GetResourceVersion(),
	}}
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if err := reconciler.rejectUnqualifiedSchedulingGroup(ctx, observed); err == nil ||
		!strings.Contains(err.Error(), "scheduling-group") {
		t.Fatalf("served scheduling-group Pod guard error = %v", err)
	}

	// A harmless metadata mutation proves that the same live API read also
	// closes the list/get race rather than evaluating stale group membership.
	if raw.GetLabels() == nil {
		raw.SetLabels(map[string]string{})
	}
	labels := raw.GetLabels()
	labels["test.cisco.vk/native-tas-guard"] = "updated"
	raw.SetLabels(labels)
	if err := apiClient.Update(ctx, raw); err != nil {
		t.Fatalf("update served Pod fixture: %v", err)
	}
	if err := reconciler.rejectUnqualifiedSchedulingGroup(ctx, observed); err == nil ||
		!strings.Contains(err.Error(), "changed during scheduling-group guard") {
		t.Fatalf("served Pod race guard error = %v", err)
	}

	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	if err := apiClient.Get(ctx, key, current); err != nil {
		t.Fatal(err)
	}
	if current.GetUID() != types.UID(observed.UID) {
		t.Fatalf("fixture Pod UID changed: %q -> %q", observed.UID, current.GetUID())
	}
	observed.ResourceVersion = current.GetResourceVersion()
	if err := reconciler.rejectUnqualifiedSchedulingGroup(ctx, observed); err == nil ||
		!strings.Contains(err.Error(), "scheduling-group") {
		t.Fatalf("current served scheduling-group Pod guard error = %v", err)
	}
}
