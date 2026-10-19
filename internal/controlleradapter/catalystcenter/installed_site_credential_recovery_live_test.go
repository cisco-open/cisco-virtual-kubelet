//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Only ordinary source/spec updates and a UID-fenced worker Pod deletion are
// permitted here. Installed actors own all journal/status transitions. Secrets
// are neither read nor projected by this assignment-only qualification.
func qualifyInstalledSiteCredentialRecovery(t *testing.T, c *client, desired []siteCredentialIntent, source []byte) *cvk.NetworkControllerConfigurationOperation {
	t.Helper()
	if len(desired) != 1 || desired[0].siteKind != "area" || !strings.HasPrefix(desired[0].site, "Global/CVK-Cred-") || source == nil {
		t.Fatal("selected generated empty-area assignment required")
	}
	selected, err := parseNetworkReportIntent(source)
	if err != nil || !reflect.DeepEqual(selected.assignments, desired) || len(selected.credentials)+len(selected.bindings)+len(selected.pools)+len(selected.reservations) != 0 {
		t.Fatal("assignment-only selected source required", err)
	}
	path := os.Getenv("CATC_INSTALLED_KUBECONFIG")
	kc, err := clientcmd.LoadFromFile(path)
	if err != nil || !filepath.IsAbs(path) || kc.CurrentContext != "kind-cvk-catc-qualification-20261017" {
		t.Fatal("explicit isolated kubeconfig required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	var system core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || string(system.UID) != os.Getenv("CATC_INSTALLED_CLUSTER_UID") {
		t.Fatal("isolated cluster identity changed")
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lifecycle"}
	var nc cvk.NetworkController
	refresh := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil || string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Spec.Paused || nc.DeletionTimestamp != nil {
			t.Fatal("pinned controller identity changed")
		}
	}
	refresh()
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		t.Fatal("idle retained journal required")
	}
	before := nc.Status.Configuration.DeepCopy()
	var devices cvk.CiscoDeviceList
	if err := api.List(ctx, &devices, ctrlclient.InNamespace(key.Namespace)); err != nil || len(devices.Items) != 0 {
		t.Fatal("device-free namespace required")
	}
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261018-site-credential-recovery-") {
		t.Fatal("source-pinned recovery image required")
	}
	depKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "catc-lifecycle-controller-worker"}
	var dep apps.Deployment
	if err := api.Get(ctx, depKey, &dep); err != nil || len(dep.Spec.Template.Spec.Containers) != 1 || dep.Spec.Template.Spec.Containers[0].Image != image || dep.Status.AvailableReplicas != 1 {
		t.Fatal("pinned recovery worker unavailable")
	}
	var config configapi.NetworkControllerConfig
	configKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "installed-noauth-recovery"}
	if err := api.Get(ctx, configKey, &config); err != nil || config.Spec.Mode != configapi.NetworkControllerApplyModeReport || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(config.Spec.SecretRefs) != 0 || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyRetain || config.Spec.TaskTimeout == nil || config.Spec.TaskTimeout.Duration != time.Nanosecond {
		t.Fatal("idle empty retained report config required")
	}
	owned := false
	for _, o := range before.Owners {
		if o.Section == "network_settings" && o.ConfigUID == string(config.UID) && o.ConfigName == config.Name {
			owned = true
		}
	}
	if !owned || config.Spec.Source.ConfigMapRef == nil {
		t.Fatal("existing ConfigMap owner required")
	}
	originalSpec := config.DeepCopy().Spec
	configUID := config.UID
	var cm core.ConfigMap
	cmKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: config.Spec.Source.ConfigMapRef.Name}
	if err := api.Get(ctx, cmKey, &cm); err != nil {
		t.Fatal(err)
	}
	sourceUID := cm.UID
	sourceKey := config.Spec.Source.ConfigMapRef.Key
	originalModel := cm.Data[sourceKey]
	intent, err := parseNetworkReportIntent([]byte(originalModel))
	if err != nil || len(intent.credentials)+len(intent.assignments)+len(intent.bindings)+len(intent.pools)+len(intent.reservations) != 0 {
		t.Fatal("empty source required before assignment qualification", err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-site-credential-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed assignment checkpoint", dir)
	save := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	read := func() {
		t.Helper()
		refresh()
		if err := api.Get(ctx, configKey, &config); err != nil || config.UID != configUID {
			t.Fatal("config replaced")
		}
		save("controller.json", &nc)
		save("config.json", &config)
	}
	poll := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("installed recovery pending; retain sources, tasks and fixture", config.Status.Conditions)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	save("before-controller.json", &nc)
	save("before-config.json", &config)
	cm.Data[sourceKey] = string(source)
	if err := api.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	if err := api.Get(ctx, configKey, &config); err != nil || config.UID != configUID {
		t.Fatal("configuration replaced before apply")
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ModelSource.Exporter = CredentialSiteSelectionExporter
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	var original *cvk.NetworkControllerConfigurationOperation
	interruptedPod := ""
	poll(func() bool {
		read()
		state := nc.Status.Configuration
		if state == nil {
			t.Fatal("journal lost")
		}
		if op := state.Operation; op != nil {
			if op.ConfigUID != string(configUID) || op.SourceUID != string(sourceUID) || op.Section != "network_settings" || op.Resource != desired[0].resource() {
				t.Fatal("foreign operation during qualification")
			}
			if original != nil && (op.ID != original.ID || op.TaskID != original.TaskID) {
				t.Fatal("original acknowledged operation replaced")
			}
			if op.Phase == "Unknown" && original == nil {
				if !validAPIID(op.TaskID) {
					t.Fatal("acknowledged task required")
				}
				original = op.DeepCopy()
				save("original-unknown.json", original)
				var pods core.PodList
				if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 {
					t.Fatal("sole worker required")
				}
				pod := pods.Items[0]
				if pod.Spec.NodeName != "cvk-catc-qualification-20261017-control-plane" || pod.DeletionTimestamp != nil || len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image {
					t.Fatal("worker identity changed")
				}
				interruptedPod = string(pod.UID)
				save("interrupted-pod.json", &pod)
				if err := api.Delete(ctx, &pod, ctrlclient.Preconditions{UID: &pod.UID}); err != nil {
					t.Fatal(err)
				}
			}
			return false
		}
		return original != nil && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation
	})
	checkTime := config.Status.LastControllerCheck.DeepCopy()
	if checkTime == nil {
		t.Fatal("authoritative native read timestamp missing")
	}
	// Require another installed observation after settlement, and an actual new
	// Ready Pod, instead of counting repeated host-side reads as no-op proof.
	poll(func() bool {
		read()
		if nc.Status.Configuration.Operation != nil {
			t.Fatal("settled assignment replayed")
		}
		var pods core.PodList
		if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil {
			t.Fatal(err)
		}
		if len(pods.Items) != 1 {
			return false
		}
		pod := pods.Items[0]
		return string(pod.UID) != interruptedPod && pod.DeletionTimestamp == nil && len(pod.Status.ContainerStatuses) == 1 && pod.Status.ContainerStatuses[0].Ready && config.Status.LastControllerCheck != nil && config.Status.LastControllerCheck.After(checkTime.Time)
	})
	if !reflect.DeepEqual(before.CredentialApplications, nc.Status.Configuration.CredentialApplications) || !reflect.DeepEqual(before.ActionApplications, nc.Status.Configuration.ActionApplications) {
		t.Fatal("unrelated historical receipts changed")
	}
	e := &siteCredentialExecutor{client: c, desired: desired}
	if ok, err := e.verify(ctx, original); err != nil || !ok {
		t.Fatal("independent original task/effect verification failed", err)
	}
	save("settled-controller.json", &nc)
	save("settled-config.json", &config)
	// Retain omission must settle through the installed actors before the caller
	// may disable the generated local slot and delete fixture definitions.
	if err := api.Get(ctx, configKey, &config); err != nil {
		t.Fatal(err)
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	if err := api.Get(ctx, cmKey, &cm); err != nil || cm.UID != sourceUID {
		t.Fatal("source replaced")
	}
	cm.Data[sourceKey] = originalModel
	if err := api.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	if err := api.Get(ctx, configKey, &config); err != nil {
		t.Fatal(err)
	}
	config.Spec = originalSpec
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	poll(func() bool {
		read()
		return config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation && nc.Status.Configuration.Operation == nil && config.Status.LastControllerCheck != nil && config.Status.LastControllerCheck.After(checkTime.Time)
	})
	save("result.json", map[string]any{"originalTask": original.TaskID, "operationID": original.ID, "target": original.TargetID, "credential": original.ParentID, "recovered": true, "kubernetesPersistence": "installed", "phaseEdits": false, "interruptedPod": interruptedPod, "historicalReceiptsPreserved": true, "sourceRestored": true, "physicalDeviceAuthentication": false})
	return original
}
