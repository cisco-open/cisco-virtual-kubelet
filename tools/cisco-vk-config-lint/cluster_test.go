// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

// fakeListerFactory builds a NamespaceableResourceInterface backed
// by client-go's fake dynamic client. The test seeds objects via the
// objects argument; the GVR is fixed to iosxeConfigGVR so the
// loader's mapping is exercised end-to-end.
func fakeListerFactory(objects ...runtime.Object) crListerFactory {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		iosxeConfigGVR: "IOSXEConfigList",
	}
	return func(_ *rest.Config) (dynamic.NamespaceableResourceInterface, error) {
		dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objects...)
		return dyn.Resource(iosxeConfigGVR), nil
	}
}

func mkUnstructuredCR(namespace, name, deviceName string, families []string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetUnstructuredContent(map[string]any{
		"apiVersion": "config.cisco.vk/v1alpha1",
		"kind":       "IOSXEConfig",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"deviceRef":       map[string]any{"name": deviceName},
			"managedFamilies": stringsToAny(families),
			"source":          map[string]any{"inline": map[string]any{}},
		},
	})
	return obj
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func TestLoadCRsFromClusterFiltersByDeviceName(t *testing.T) {
	// Three CRs in one namespace. Two target edge-01 (kept), one
	// targets edge-02 (filtered out) — proves deviceName filtering
	// is the same in both loaders.
	objs := []runtime.Object{
		mkUnstructuredCR("netcfg", "uplinks", "edge-01", []string{"vlan", "vrf"}),
		mkUnstructuredCR("netcfg", "routing", "edge-01", []string{"bgp"}),
		mkUnstructuredCR("netcfg", "other-device", "edge-02", []string{"vlan"}),
	}
	got, err := loadCRsFromCluster(
		context.Background(), &rest.Config{}, "netcfg", false, "edge-01",
		fakeListerFactory(objs...))
	if err != nil {
		t.Fatalf("loadCRsFromCluster: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d CRs, want 2", len(got))
	}
	names := []string{got[0].FullName, got[1].FullName}
	sort.Strings(names)
	want := []string{"netcfg/routing", "netcfg/uplinks"}
	if names[0] != want[0] || names[1] != want[1] {
		t.Errorf("got names=%v, want %v", names, want)
	}
}

func TestLoadCRsFromClusterAllNamespaces(t *testing.T) {
	// CRs spread across two namespaces with the same deviceName.
	// --all-namespaces must return both; a namespaced read scoped
	// to one of them must return only one.
	objs := []runtime.Object{
		mkUnstructuredCR("ns-a", "uplinks", "edge-01", []string{"vlan"}),
		mkUnstructuredCR("ns-b", "routing", "edge-01", []string{"bgp"}),
	}
	all, err := loadCRsFromCluster(
		context.Background(), &rest.Config{}, "", true, "edge-01",
		fakeListerFactory(objs...))
	if err != nil {
		t.Fatalf("all-namespaces: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all-namespaces returned %d, want 2", len(all))
	}

	scoped, err := loadCRsFromCluster(
		context.Background(), &rest.Config{}, "ns-a", false, "edge-01",
		fakeListerFactory(objs...))
	if err != nil {
		t.Fatalf("scoped: %v", err)
	}
	if len(scoped) != 1 || scoped[0].FullName != "ns-a/uplinks" {
		t.Fatalf("scoped returned %#v", scoped)
	}
}

func TestLoadCRsFromClusterEmptyListIsNotAnError(t *testing.T) {
	// No matching CRs is a valid input — every device family will
	// then surface as an orphan, which is exactly what an
	// "introduce CVK to a brownfield device" workflow needs.
	got, err := loadCRsFromCluster(
		context.Background(), &rest.Config{}, "netcfg", false, "edge-01",
		fakeListerFactory())
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d CRs, want 0", len(got))
	}
}

// Compile-time sanity: ensure metav1 import is used so a future
// refactor that drops mkUnstructuredCR doesn't accidentally orphan
// the import.
var _ = metav1.ObjectMeta{}
