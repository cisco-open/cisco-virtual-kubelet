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
	"fmt"
	"os"
	"os/exec"
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
	t.Run("native Deployment preserves group on member replacement", func(t *testing.T) {
		qualifyNativeGroupOwner(t, apiClient, namespace)
	})
}

// A native owner, not this test, creates replacement members. These synthetic
// Nodes have no application runtime: binding is not app readiness, service
// continuity, group relocation or permission for CVK to drain such groups.
func qualifyNativeGroupOwner(t *testing.T, c client.Client, namespace string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const name = "cvk-native-owner-probe"
	group := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "scheduling.k8s.io/v1beta1", "kind": "PodGroup",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"schedulingPolicy":      map[string]interface{}{"gang": map[string]interface{}{"minCount": int64(2)}},
			"schedulingConstraints": map[string]interface{}{"topology": []interface{}{map[string]interface{}{"key": "topology.cisco.vk/site"}}},
		},
	}}
	if err := c.Create(ctx, group); err != nil {
		t.Fatalf("create owned group fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := c.Delete(cleanup, group); client.IgnoreNotFound(err) != nil {
			t.Errorf("delete owned group fixture: %v", err)
		}
	})
	labels := map[string]interface{}{"test.cisco.vk/native-owner": name}
	owner := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"replicas": int64(2), "selector": map[string]interface{}{"matchLabels": labels},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{"labels": labels},
				"spec": map[string]interface{}{
					"schedulingGroup": map[string]interface{}{"podGroupName": name},
					"nodeSelector":    map[string]interface{}{"test.cisco.vk/native-tas": "true"},
					"containers": []interface{}{map[string]interface{}{
						"name": "pause", "image": "registry.k8s.io/pause:3.10",
						"resources": map[string]interface{}{"requests": map[string]interface{}{"cpu": "100m", "memory": "128Mi"}},
					}},
				},
			},
		},
	}}
	if err := c.Create(ctx, owner); err != nil {
		t.Fatalf("native Deployment rejected group template: %v", err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := c.Delete(cleanup, owner, client.PropagationPolicy(metav1.DeletePropagationForeground)); client.IgnoreNotFound(err) != nil {
			t.Errorf("delete owned Deployment fixture: %v", err)
		}
	})
	waitMembers := func(retired types.UID) []unstructured.Unstructured {
		t.Helper()
		var detail string
		for ctx.Err() == nil {
			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "PodList"})
			if err := c.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{"test.cisco.vk/native-owner": name}); err != nil {
				t.Fatalf("read native owner members: %v", err)
			}
			ready, node := len(list.Items) == 2, ""
			for i := range list.Items {
				pod := &list.Items[i]
				bound, _, _ := unstructured.NestedString(pod.Object, "spec", "nodeName")
				membership, _, _ := unstructured.NestedString(pod.Object, "spec", "schedulingGroup", "podGroupName")
				if membership != name {
					t.Fatal("native owner lost scheduling-group membership")
				}
				if pod.GetUID() == retired || pod.GetDeletionTimestamp() != nil || bound == "" {
					ready = false
				}
				if node != "" && bound != "" && node != bound {
					t.Fatal("native owner members bound across single-node site domains")
				}
				if bound != "" {
					node = bound
				}
				rsRef := metav1.GetControllerOf(pod)
				if rsRef == nil || rsRef.Kind != "ReplicaSet" {
					t.Fatal("member was not created by a native ReplicaSet")
				}
				rs := &unstructured.Unstructured{}
				rs.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"})
				if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: rsRef.Name}, rs); err != nil {
					t.Fatal(err)
				}
				deploymentRef := metav1.GetControllerOf(rs)
				if rs.GetUID() != rsRef.UID || deploymentRef == nil || deploymentRef.UID != owner.GetUID() {
					t.Fatal("member owner chain does not bind the original Deployment")
				}
			}
			if ready {
				return list.Items
			}
			detail = fmt.Sprintf("members=%d, selectedNode=%s", len(list.Items), node)
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
		}
		t.Fatalf("native owner members did not converge: %s: %v", detail, ctx.Err())
		return nil
	}
	initial := waitMembers("")
	// Stop the actual native owner-controller process, not its mirror Pod.
	// The caller already checked this is the explicitly selected kind context.
	cluster := strings.TrimPrefix(os.Getenv("CVK_NATIVE_TAS_TEST_CONTEXT"), "kind-")
	controlPlane := cluster + "-control-plane"
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("native owner process probe failed: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if docker("inspect", "--format", `{{ index .Config.Labels "io.x-k8s.kind.cluster" }}`, controlPlane) != cluster {
		t.Fatal("refusing to restart a container outside the disposable kind cluster")
	}
	process := docker("exec", controlPlane, "crictl", "ps", "--state", "Running", "--name", "kube-controller-manager", "-q")
	if len(strings.Fields(process)) != 1 {
		t.Fatal("expected exactly one native controller-manager container")
	}
	docker("exec", controlPlane, "crictl", "stop", process)
	restarted := false
	for ctx.Err() == nil {
		next := docker("exec", controlPlane, "crictl", "ps", "--state", "Running", "--name", "kube-controller-manager", "-q")
		if len(strings.Fields(next)) == 1 && next != process {
			restarted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !restarted {
		t.Fatal("native controller-manager process did not restart")
	}
	retired := initial[0].GetUID()
	if err := c.Delete(ctx, &initial[0], client.GracePeriodSeconds(0), client.Preconditions{UID: &retired}); err != nil {
		t.Fatalf("delete exact synthetic member: %v", err)
	}
	waitMembers(retired)
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(group.GroupVersionKind())
	if err := c.Get(ctx, client.ObjectKeyFromObject(group), current); err != nil || current.GetUID() != group.GetUID() {
		t.Fatalf("replacement did not retain the original group identity: %v", err)
	}
	t.Log("native Deployment/ReplicaSet created members, survived controller-process restart, and replaced a member with unchanged PodGroup identity; synthetic scheduling only")
}
