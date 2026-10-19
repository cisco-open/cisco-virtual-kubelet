//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The harness writes only generated intent/metadata and deletes an exact worker
// Pod. Installed actors own all journals, attestations and native writes. No
// status transition, reconciler invocation or fabricated task is used here.
func TestLiveInstalledDiscoveryHandoff(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_DISCOVERY_HANDOFF") != "isolated-two-incarnations" {
		t.Skip("explicit isolated installed handoff qualification required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	var system, namespace core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || system.UID == "" || string(system.UID) != os.Getenv("CATC_INSTALLED_CLUSTER_UID") {
		t.Fatal("cluster identity changed", err)
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-handoff-20261018", Name: "catc-handoff"}
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: key.Namespace}, &namespace); err != nil || namespace.UID == "" || string(namespace.UID) != os.Getenv("CATC_INSTALLED_NAMESPACE_UID") {
		t.Fatal("namespace identity changed", err)
	}
	var nc cvk.NetworkController
	refresh := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil || nc.UID == "" || string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Spec.Paused || nc.DeletionTimestamp != nil {
			t.Fatal("pinned controller identity changed", err)
		}
	}
	refresh()
	if s := nc.Status.Configuration; s != nil && (s.Operation != nil || len(s.Owners)+len(s.ActionApplications)+len(s.CredentialApplications)+len(s.GroupCancellations) != 0) {
		t.Fatal("fresh isolated controller required; existing ownership must not be removed")
	}
	var devices cvk.CiscoDeviceList
	var configs configapi.NetworkControllerConfigList
	if err := api.List(ctx, &devices, ctrlclient.InNamespace(key.Namespace)); err != nil || len(devices.Items) != 0 {
		t.Fatal("device-free namespace required", err)
	}
	if err := api.List(ctx, &configs, ctrlclient.InNamespace(key.Namespace)); err != nil || len(configs.Items) != 0 {
		t.Fatal("config-free namespace required", err)
	}
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261018-discovery-handoff-") {
		t.Fatal("source-pinned handoff image required")
	}
	depKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: key.Name + "-controller-worker"}
	var dep apps.Deployment
	if err := api.Get(ctx, depKey, &dep); err != nil || dep.Status.AvailableReplicas != 1 || len(dep.Spec.Template.Spec.Containers) != 1 || dep.Spec.Template.Spec.Containers[0].Image != image {
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
	if !ok || seed.Status != "Inactive" || seed.Condition != "Complete" {
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
			t.Fatal("seed credential description unavailable")
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("seed credential references required")
	}
	inventory, err := observer.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, inventory)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-discovery-handoff-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed handoff checkpoint", dir)
	save := func(name string, value any) { t.Helper(); writeLiveDiscoveryPublicRecord(t, dir, name, value) }
	save("before-controller.json", &nc)
	save("before-discoveries.json", baseline)
	name := wirelessQualificationName(t, "CVK_INST_HANDOFF_")
	objectName := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	model, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": []any{map[string]any{"name": name, "type": "SINGLE", "ip_address_list": "198.51.100.101", "protocol_order": "SSH", "global_credential_list": names}}}}})
	if err != nil {
		t.Fatal(err)
	}
	const keeper = "test.cisco.vk/installed-handoff-qualification"
	var c configapi.NetworkControllerConfig
	poll := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("installed handoff pending; preserve original task, source, ownership and checkpoint", dir)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	deleteExact := func(obj ctrlclient.Object) {
		t.Helper()
		uid := obj.GetUID()
		objectKey := ctrlclient.ObjectKeyFromObject(obj)
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := obj.DeepCopyObject().(ctrlclient.Object)
			if err := api.Get(ctx, objectKey, current); err != nil {
				return err
			}
			if current.GetUID() != uid {
				return errConfigurationStale
			}
			rv := current.GetResourceVersion()
			return api.Delete(ctx, current, ctrlclient.Preconditions{UID: &uid, ResourceVersion: &rv})
		}); err != nil {
			t.Fatal("UID-fenced deletion refused; fixture retained", err)
		}
	}
	restart := func(stage int) types.UID {
		t.Helper()
		var current apps.Deployment
		if err := api.Get(ctx, depKey, &current); err != nil || current.UID != depUID || len(current.Spec.Template.Spec.Containers) != 1 || current.Spec.Template.Spec.Containers[0].Image != image {
			t.Fatal("worker deployment replaced", err)
		}
		var pods core.PodList
		if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels(current.Spec.Selector.MatchLabels)); err != nil || len(pods.Items) != 1 {
			t.Fatal("one exact worker Pod required", err)
		}
		pod := pods.Items[0]
		if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image || len(pod.OwnerReferences) != 1 {
			t.Fatal("worker Pod image/ownership changed")
		}
		var rs apps.ReplicaSet
		if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: pod.Namespace, Name: pod.OwnerReferences[0].Name}, &rs); err != nil || rs.UID != pod.OwnerReferences[0].UID || len(rs.OwnerReferences) != 1 || rs.OwnerReferences[0].UID != depUID {
			t.Fatal("worker owner chain changed", err)
		}
		save(fmt.Sprintf("stage-%d-interrupted-pod.json", stage), &pod)
		deleteExact(&pod)
		return pod.UID
	}
	var priorReceipts []cvk.NetworkControllerConfigurationActionApplication
	var previousConfig, previousSource types.UID
	var previousNative string
	for stage := 1; stage <= 2; stage++ {
		cm := core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: objectName}, Data: map[string]string{"model": string(model)}}
		c = configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: objectName, Finalizers: []string{keeper}}, Spec: configapi.NetworkControllerConfigSpec{
			ControllerRef: configapi.NetworkControllerRef{Name: key.Name}, Scope: "global", ManagedSections: []string{"inventory"}, Mode: configapi.NetworkControllerApplyModeApply,
			PrunePolicy: configapi.NetworkControllerRetentionPolicyRetain, DeletionPolicy: configapi.NetworkControllerRetentionPolicyDelete, TaskTimeout: &metav1.Duration{Duration: time.Nanosecond},
			ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true, Exporter: "cvk-installed-discovery-handoff-v1"},
			Source:      configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}},
		}}
		save(fmt.Sprintf("stage-%d-prepared.json", stage), map[string]any{"config": c, "source": cm, "image": image, "namespaceUID": namespace.UID, "controllerUID": nc.UID})
		if err := api.Create(ctx, &cm); err != nil {
			t.Fatal(err)
		}
		save(fmt.Sprintf("stage-%d-source.json", stage), &cm)
		if err := api.Create(ctx, &c); err != nil {
			t.Fatal("source retained for inspection", err)
		}
		configUID := c.UID
		if stage == 2 && (configUID == previousConfig || cm.UID == previousSource) {
			t.Fatal("successor config/source identity reused")
		}
		read := func() {
			t.Helper()
			refresh()
			if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&c), &c); err != nil || c.UID != configUID {
				t.Fatal("config replaced/lost", err)
			}
			save(fmt.Sprintf("stage-%d-controller.json", stage), &nc)
			save(fmt.Sprintf("stage-%d-config.json", stage), &c)
		}
		var original *cvk.NetworkControllerConfigurationOperation
		poll(func() bool {
			read()
			state := nc.Status.Configuration
			if state == nil || state.Operation == nil || state.Operation.ConfigUID != string(configUID) || state.Operation.Phase != "Unknown" {
				return false
			}
			op := state.Operation
			if op.Action != "Create" || op.TaskID == "" || op.SourceUID != string(cm.UID) || !hasDiscoveryConfigFinalizer(&c) || !meta.IsStatusConditionTrue(c.Status.Conditions, discoveryScopeCondition) {
				t.Fatal("enrolled source-bound acknowledged task required")
			}
			if len(state.ActionApplications) != stage-1 {
				t.Fatal("unexpected receipt count before successor creation")
			}
			for i, r := range priorReceipts {
				if !reflect.DeepEqual(state.ActionApplications[i], r) {
					t.Fatal("handoff changed predecessor evidence")
				}
			}
			original = op.DeepCopy()
			return true
		})
		save(fmt.Sprintf("stage-%d-original-unknown.json", stage), original)
		if stage == 1 {
			deleteExact(&cm)
			deleteExact(&c)
		}
		oldPod := restart(stage)
		if stage == 2 {
			poll(func() bool {
				read()
				state := nc.Status.Configuration
				if op := state.Operation; op != nil && op.ConfigUID == string(configUID) && (op.ID != original.ID || op.TaskID != original.TaskID) {
					t.Fatal("successor original task changed across restart")
				}
				return state.Operation == nil && c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync
			})
			state := nc.Status.Configuration
			var created *cvk.NetworkControllerConfigurationActionApplication
			for i := range state.ActionApplications {
				if state.ActionApplications[i].Operation.ConfigUID == string(configUID) {
					created = &state.ActionApplications[i]
				}
			}
			if created == nil || !validOwnedDiscoveryReceipt(*created) || created.Operation.Action != "Create" || created.Operation.TaskID != original.TaskID {
				t.Fatal("successor create receipt unavailable")
			}
			rows, err := observer.discoveries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			job, ok := rows[name]
			if !ok || job.ID == previousNative || len(rows) != len(baseline)+1 {
				t.Fatal("same-name successor did not establish a distinct native incarnation")
			}
			save("successor-in-sync.json", map[string]any{"config": c, "receipt": created, "nativeUUID": job.ID})
			deleteExact(&cm)
			deleteExact(&c)
		}
		poll(func() bool {
			read()
			state := nc.Status.Configuration
			if op := state.Operation; op != nil && op.ConfigUID == string(configUID) && !discoveryFinalizeSelected(op.Resource) && (op.ID != original.ID || op.TaskID != original.TaskID) {
				t.Fatal("pre-deletion task identity changed")
			}
			return !hasDiscoveryConfigFinalizer(&c) && meta.IsStatusConditionTrue(c.Status.Conditions, discoveryConfigCleanupCondition)
		})
		state := nc.Status.Configuration
		if state.Operation != nil || !slices.Equal(c.Finalizers, []string{keeper}) || len(state.ActionApplications) != stage || configurationOwnersMatch(state, &c) != nil {
			t.Fatal("manager release/history boundary incomplete")
		}
		var receipt *cvk.NetworkControllerConfigurationActionApplication
		for i := range state.ActionApplications {
			r := &state.ActionApplications[i]
			if !validOwnedDiscoveryReceipt(*r) || r.Operation.Action != "Delete" {
				t.Fatal("incomplete deletion history")
			}
			if r.Operation.ConfigUID == string(configUID) {
				if receipt != nil {
					t.Fatal("duplicate owned deletion")
				}
				receipt = r
			}
		}
		if receipt == nil || receipt.Operation.TargetID == "" || receipt.Operation.TaskID == "" || stage == 2 && receipt.Operation.TargetID == previousNative {
			t.Fatal("exact successor deletion receipt missing")
		}
		for i, r := range priorReceipts {
			if !reflect.DeepEqual(state.ActionApplications[i], r) {
				t.Fatal("successor cleanup relabeled predecessor history")
			}
		}
		if _, _, err := discoveryRetirementRecord(&nc); err != nil {
			t.Fatal("installed retirement attestation invalid", err)
		}
		for range 2 {
			rows, err := observer.discoveries(ctx)
			if err != nil || !reflect.DeepEqual(rows, baseline) {
				t.Fatal("native baseline not restored", err)
			}
		}
		var pods core.PodList
		if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels(dep.Spec.Selector.MatchLabels)); err != nil || len(pods.Items) != 1 || pods.Items[0].UID == oldPod || pods.Items[0].Spec.Containers[0].Image != image {
			t.Fatal("new pinned worker incarnation not observed", err)
		}
		save(fmt.Sprintf("stage-%d-recovered-pod.json", stage), &pods.Items[0])
		save(fmt.Sprintf("stage-%d-completed.json", stage), map[string]any{"config": c, "controller": nc, "sourceRemoved": true, "originalTask": original, "deleteReceipt": receipt, "managerReleased": true, "nativeBaselineRestored": true})
		priorReceipts = state.DeepCopy().ActionApplications
		previousConfig, previousSource, previousNative = configUID, cm.UID, receipt.Operation.TargetID
		// Release only our own keeper, after durable evidence and native checks.
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var current configapi.NetworkControllerConfig
			if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&c), &current); err != nil {
				return err
			}
			if current.UID != configUID || !slices.Equal(current.Finalizers, []string{keeper}) || !meta.IsStatusConditionTrue(current.Status.Conditions, discoveryConfigCleanupCondition) {
				return errConfigurationStale
			}
			base := current.DeepCopy()
			current.Finalizers = nil
			return api.Patch(ctx, &current, ctrlclient.MergeFromWithOptions(base, ctrlclient.MergeFromWithOptimisticLock{}))
		}); err != nil {
			t.Fatal("keeper retained for investigation", err)
		}
		poll(func() bool {
			var current configapi.NetworkControllerConfig
			err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&c), &current)
			if apierrors.IsNotFound(err) {
				return true
			}
			if err != nil {
				t.Fatal(err)
			}
			if current.UID != configUID {
				t.Fatal("unexpected external replacement")
			}
			return false
		})
	}
	after, err := observer.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, after)
	if err != nil || len(after) != len(inventory) || afterTarget.ID != target.ID || afterTarget.Serial != target.Serial {
		t.Fatal("lab inventory identity changed", err)
	}
	save("qualified.json", map[string]any{"installedActors": true, "statusInjected": false, "incarnations": 2, "sameConfigAndJobNames": true, "sourceLossBothCycles": true, "naturalCreateUnknownBothCycles": true, "workerRestartBothCycles": true, "originalTasksRetained": true, "predecessorReceiptsPreserved": true, "managerReleasedBothCycles": true, "nativeBaselineRestored": true, "inventoryIdentityPreserved": true, "retirementAttestationValid": true, "finalJournal": nc.Status.Configuration})
	t.Log("installed source-loss and same-name ownership handoff verified across two restarted worker incarnations", dir)
}
