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

//go:build envtest

package controller

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// TestEnvtest_RolloutLeafSpecIsAPIRoundTripStable covers the real API-server
// defaulting boundary that fake clients omit. The rollout manager compares a
// stored leaf spec at three authority/fencing points, so its generated spec
// must already contain every CRD default.
func TestEnvtest_RolloutLeafSpecIsAPIRoundTripStable(t *testing.T) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{findControllerCRDPath(t)},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest start: %v (is KUBEBUILDER_ASSETS set?)", err)
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("envtest stop: %v", err)
		}
	}()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))
	apiClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const namespace = "rollout-leaf-defaults"
	if err := apiClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	target := policyFenceTarget("edge-a", "device-uid", "campaign-edge-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Namespace = namespace
	expected := expectedLeafSpec(rollout, target)
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: target.ChildName},
		Spec:       expected,
	}
	if err := apiClient.Create(ctx, leaf); err != nil {
		t.Fatalf("create generated rollout leaf: %v", err)
	}
	var stored opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: leaf.Name}, &stored); err != nil {
		t.Fatalf("read generated rollout leaf: %v", err)
	}
	if !reflect.DeepEqual(stored.Spec, expected) {
		t.Fatalf("API-round-tripped leaf spec differs from generated spec:\n stored: %#v\nexpected: %#v", stored.Spec, expected)
	}
}

func findControllerCRDPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "config", "crd")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate config/crd above %s", dir)
		}
		dir = parent
	}
}
