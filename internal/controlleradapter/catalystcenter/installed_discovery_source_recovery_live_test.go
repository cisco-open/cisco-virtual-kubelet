//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Installed actors own every status/journal transition and native write. The
// harness only creates its own intent, removes that source, requests deletion,
// and restarts the exact worker Pod after a naturally timed-out accepted task.
// Existing config sources, Kubernetes Secrets and projected comparison keys are
// not read. The read-only native observer uses the separately supplied API login.
func TestLiveInstalledDiscoverySourceRecovery(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_DISCOVERY_RECOVERY") != "isolated-source-loss" {
		t.Skip("explicit isolated installed discovery qualification required")
	}
	path := os.Getenv("CATC_INSTALLED_KUBECONFIG")
	kc, err := clientcmd.LoadFromFile(path)
	if err != nil || !filepath.IsAbs(path) || kc.CurrentContext != "kind-cvk-catc-qualification-20261017" {
		t.Fatal("pinned isolated kubeconfig required", err)
	}
	rest, err := clientcmd.NewDefaultClientConfig(*kc, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{core.AddToScheme, apps.AddToScheme, cvk.AddToScheme, configapi.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	api, err := ctrlclient.New(rest, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	var system core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || string(system.UID) != os.Getenv("CATC_INSTALLED_CLUSTER_UID") || system.UID == "" {
		t.Fatal("isolated cluster identity changed", err)
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lifecycle"}
	var nc cvk.NetworkController
	refresh := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil || nc.UID == "" || string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Spec.Paused || nc.DeletionTimestamp != nil {
			t.Fatal("pinned controller identity changed", err)
		}
	}
	refresh()
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		t.Fatal("idle retained journal required")
	}
	before := nc.Status.Configuration.DeepCopy()
	for _, owner := range before.Owners {
		if owner.Section == "inventory" {
			t.Fatal("inventory already owned; retained evidence must not be erased")
		}
	}
	var devices cvk.CiscoDeviceList
	if err := api.List(ctx, &devices, ctrlclient.InNamespace(key.Namespace)); err != nil || len(devices.Items) != 0 {
		t.Fatal("device-free namespace required", err)
	}
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261018-discovery-source-") {
		t.Fatal("source-pinned discovery image required")
	}
	depKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "catc-lifecycle-controller-worker"}
	var dep apps.Deployment
	if err := api.Get(ctx, depKey, &dep); err != nil || len(dep.Spec.Template.Spec.Containers) != 1 || dep.Spec.Template.Spec.Containers[0].Image != image || dep.Status.AvailableReplicas != 1 {
		t.Fatal("pinned worker unavailable", err)
	}
	depUID := dep.UID
	observer := liveClient(t)
	session, _, err := observer.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transport := session.HTTPClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	session.HTTPClient.Transport = &siteRetainedReadOnlyTransport{base: transport}
	baseline, err := observer.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed, ok := baseline["Switches"]
	if !ok || seed.Condition != "Complete" || seed.Status != "Inactive" {
		t.Fatal("completed lab seed required")
	}
	credentials, err := observer.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, id := range seed.CredentialIDs {
		name := ""
		for _, group := range credentials {
			for _, cred := range group {
				if cred.ID == id {
					if name != "" {
						t.Fatal("ambiguous credential identity")
					}
					name = cred.Name
				}
			}
		}
		if name == "" {
			t.Fatal("seed credential identity unavailable")
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("seed has no approved credential references")
	}
	inventory, err := observer.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, inventory)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-discovery-source-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed recovery checkpoint", dir)
	save := func(name string, value any) { t.Helper(); writeLiveDiscoveryPublicRecord(t, dir, name, value) }
	save("before-journal.json", before)
	save("before-discoveries.json", baseline)
	name := wirelessQualificationName(t, "CVK_INST_DISC_")
	model, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": []any{map[string]any{"name": name, "type": "SINGLE", "ip_address_list": "198.51.100.101", "protocol_order": "SSH", "global_credential_list": names}}}}})
	if err != nil {
		t.Fatal(err)
	}
	const keeper = "test.cisco.vk/installed-discovery-qualification"
	objectName := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	cm := core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: objectName}, Data: map[string]string{"model": string(model)}}
	config := configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: objectName, Finalizers: []string{keeper}}, Spec: configapi.NetworkControllerConfigSpec{
		ControllerRef: configapi.NetworkControllerRef{Name: key.Name}, Scope: "global", ManagedSections: []string{"inventory"},
		Mode: configapi.NetworkControllerApplyModeApply, PrunePolicy: configapi.NetworkControllerRetentionPolicyRetain, DeletionPolicy: configapi.NetworkControllerRetentionPolicyDelete,
		TaskTimeout: &metav1.Duration{Duration: time.Nanosecond},
		ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true, Exporter: "cvk-installed-discovery-source-v1"},
		Source:      configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}},
	}}
	save("prepared.json", map[string]any{"name": name, "config": config, "source": cm, "controllerUID": nc.UID, "clusterUID": system.UID, "image": image, "installedActors": true})
	if err := api.Create(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	save("source-created.json", &cm)
	if err := api.Create(ctx, &config); err != nil {
		t.Fatal("source retained for investigation", err)
	}
	configUID := config.UID
	configKey := ctrlclient.ObjectKeyFromObject(&config)
	read := func() {
		t.Helper()
		refresh()
		if err := api.Get(ctx, configKey, &config); err != nil || config.UID != configUID {
			t.Fatal("qualification config lost/replaced", err)
		}
		save("controller.json", &nc)
		save("config.json", &config)
	}
	poll := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("installed qualification pending; preserve source, config, original task and checkpoint", dir)
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	var original *cvk.NetworkControllerConfigurationOperation
	poll(func() bool {
		read()
		op := nc.Status.Configuration.Operation
		if op == nil || op.ConfigUID != string(configUID) || op.Phase != "Unknown" {
			return false
		}
		if op.Action != "Create" || op.TaskID == "" || op.SourceUID != string(cm.UID) || !hasDiscoveryConfigFinalizer(&config) || !meta.IsStatusConditionTrue(config.Status.Conditions, discoveryScopeCondition) {
			t.Fatal("original acknowledged Create lacks enrolled source proof")
		}
		original = op.DeepCopy()
		return true
	})
	save("original-unknown.json", original)
	// Use explicit UID+resourceVersion preconditions, including the test-only
	// restart. No automatic cleanup or task replay is attempted on failure.
	if err := api.Delete(ctx, &cm, ctrlclient.Preconditions{UID: &cm.UID, ResourceVersion: &cm.ResourceVersion}); err != nil {
		t.Fatal(err)
	}
	if err := api.Delete(ctx, &config, ctrlclient.Preconditions{UID: &config.UID, ResourceVersion: &config.ResourceVersion}); err != nil {
		t.Fatal(err)
	}
	var pods core.PodList
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels(dep.Spec.Selector.MatchLabels)); err != nil || len(pods.Items) != 1 {
		t.Fatal("one pinned worker Pod required", err)
	}
	pod := pods.Items[0]
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image || len(pod.OwnerReferences) != 1 {
		t.Fatal("worker Pod image/owner changed")
	}
	var rs apps.ReplicaSet
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: pod.Namespace, Name: pod.OwnerReferences[0].Name}, &rs); err != nil || rs.UID != pod.OwnerReferences[0].UID || len(rs.OwnerReferences) != 1 || rs.OwnerReferences[0].UID != depUID {
		t.Fatal("worker Pod not owned by pinned Deployment", err)
	}
	save("interrupted-pod.json", &pod)
	if err := api.Delete(ctx, &pod, ctrlclient.Preconditions{UID: &pod.UID, ResourceVersion: &pod.ResourceVersion}); err != nil {
		t.Fatal(err)
	}
	poll(func() bool {
		read()
		state := nc.Status.Configuration
		if state.Operation != nil && state.Operation.ConfigUID == string(configUID) && !discoveryFinalizeSelected(state.Operation.Resource) && (state.Operation.ID != original.ID || state.Operation.TaskID != original.TaskID) {
			t.Fatal("original task changed across restart")
		}
		return !hasDiscoveryConfigFinalizer(&config) && meta.IsStatusConditionTrue(config.Status.Conditions, discoveryConfigCleanupCondition)
	})
	state := nc.Status.Configuration
	if state.Operation != nil || !slices.Equal(config.Finalizers, []string{keeper}) {
		t.Fatal("manager release boundary incomplete")
	}
	var receipt *cvk.NetworkControllerConfigurationActionApplication
	for i := range state.ActionApplications {
		r := &state.ActionApplications[i]
		if r.Operation.ConfigUID == string(configUID) {
			if receipt != nil {
				t.Fatal("duplicate owned receipt")
			}
			receipt = r
		}
	}
	if receipt == nil || !validOwnedDiscoveryReceipt(*receipt) || receipt.Operation.Action != "Delete" || receipt.Operation.TaskID == "" || receipt.Operation.TargetID == "" {
		t.Fatal("exact native deletion receipt missing")
	}
	// Other sections retain their original receipts and owners.
	foreign := state.DeepCopy()
	foreign.Owners = slices.DeleteFunc(foreign.Owners, func(o cvk.NetworkControllerSectionOwner) bool { return o.ConfigUID == string(configUID) })
	foreign.ActionApplications = slices.DeleteFunc(foreign.ActionApplications, func(r cvk.NetworkControllerConfigurationActionApplication) bool {
		return r.Operation.ConfigUID == string(configUID)
	})
	if !reflect.DeepEqual(foreign, before) {
		t.Fatal("unrelated retained journal changed")
	}
	for range 2 {
		rows, err := observer.discoveries(ctx)
		if err != nil || !reflect.DeepEqual(rows, baseline) {
			t.Fatal("native discovery baseline not restored", err)
		}
	}
	after, err := observer.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, after)
	if err != nil || len(after) != len(inventory) || afterTarget.ID != target.ID || afterTarget.Serial != target.Serial {
		t.Fatal("lab inventory identity changed", err)
	}
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels(dep.Spec.Selector.MatchLabels)); err != nil || len(pods.Items) != 1 || pods.Items[0].UID == pod.UID || pods.Items[0].Spec.Containers[0].Image != image {
		t.Fatal("new pinned worker process not observed", err)
	}
	save("recovered-pod.json", &pods.Items[0])
	save("final-journal.json", state)
	save("final-config.json", &config)
	save("qualified.json", map[string]any{"installedActors": true, "statusInjected": false, "originalOperation": original.ID, "originalTask": original.TaskID, "deletedUUID": receipt.Operation.TargetID, "deleteTask": receipt.Operation.TaskID, "managerReleased": true, "sourceRemoved": true, "nativeBaselineRestored": true, "inventoryIdentityPreserved": true, "ownerAndTombstoneRetained": true})
	// Remove only our keeper after all native and manager evidence is durable.
	base := config.DeepCopy()
	config.Finalizers = nil
	if err := api.Patch(ctx, &config, ctrlclient.MergeFromWithOptions(base, ctrlclient.MergeFromWithOptimisticLock{})); err != nil {
		t.Fatal(err)
	}
	poll(func() bool {
		var current configapi.NetworkControllerConfig
		err := api.Get(ctx, configKey, &current)
		if apierrors.IsNotFound(err) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		return false
	})
	t.Log("installed source-loss recovery and manager-only finalization verified; owner/tombstone retained", receipt.Operation.TargetID)
}
