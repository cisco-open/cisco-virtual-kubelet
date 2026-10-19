//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The host may observe and clean only the terminal generated UUID. All create,
// update and task-recovery writes are performed by the installed worker.
type installedCredentialObserver struct {
	base    http.RoundTripper
	cleanup string
	deletes int
}

func (o *installedCredentialObserver) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.URL.Path != authPath {
		if r.Method != http.MethodDelete || o.cleanup == "" || r.URL.Path != globalCredentialsPath+"/"+o.cleanup || o.deletes != 0 {
			return nil, errors.New("installed qualification host mutation refused")
		}
		o.deletes++
	}
	return o.base.RoundTrip(r)
}

func TestLiveInstalledMetadataCredentialRecovery(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_CREDENTIAL_RECOVERY") != "isolated-unused-noauth" {
		t.Skip("installed metadata recovery disabled")
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
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	var system core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || string(system.UID) != os.Getenv("CATC_INSTALLED_CLUSTER_UID") {
		t.Fatal("isolated cluster identity changed")
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lifecycle"}
	var nc cvk.NetworkController
	refresh := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil {
			t.Fatal(err)
		}
		if string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.DeletionTimestamp != nil || nc.Spec.Paused || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Spec.Type != "catalyst-center" {
			t.Fatal("controller identity changed")
		}
	}
	refresh()
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		t.Fatal("idle existing site owner required")
	}
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == "network_settings" {
			t.Fatal("network settings already owned; never replay test")
		}
	}
	baselineJournal := nc.Status.Configuration.DeepCopy()
	var devices cvk.CiscoDeviceList
	if err := api.List(ctx, &devices, ctrlclient.InNamespace(key.Namespace)); err != nil || len(devices.Items) != 0 {
		t.Fatal("qualification namespace must have no CiscoDevices")
	}
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	var dep apps.Deployment
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261018-credential-recovery-") {
		t.Fatal("source-pinned credential image required")
	}
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "catc-lifecycle-controller-worker"}, &dep); err != nil || len(dep.Spec.Template.Spec.Containers) != 1 || dep.Spec.Template.Spec.Containers[0].Image != image || dep.Status.AvailableReplicas != 1 {
		t.Fatal("qualification worker unavailable", err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-credential-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed credential evidence", dir)
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
	save("baseline-controller.json", &nc)
	c := liveClient(t)
	observer := &installedCredentialObserver{}
	c.mu.Lock()
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		observer.base = base
		return observer
	}
	c.mu.Unlock()
	baseline, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	save("baseline-native.json", baseline)
	name := "CVK_INST_NOAUTH_" + time.Now().UTC().Format("20060102_150405")
	if _, exists := baseline["snmpv3_credentials"][name]; exists {
		t.Fatal("generated definition exists")
	}
	cm := core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "installed-credential-model-" + strings.ToLower(time.Now().UTC().Format("150405")), Namespace: key.Namespace}, Data: map[string]string{"model.json": string(noAuthCredentialModel(name, "cvkInstalledOne"))}}
	if err := api.Create(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := json.Unmarshal([]byte(`{"apiVersion":"config.cisco.vk/v1alpha1","kind":"NetworkControllerConfig","metadata":{"name":"installed-noauth-recovery","namespace":"cvk-catc-qualification"},"spec":{"controllerRef":{"name":"catc-lifecycle"},"managedSections":["network_settings"],"scope":"global","mode":"report","prunePolicy":"Retain","deletionPolicy":"Retain","driftDetectInterval":"30s","taskTimeout":"1ns","modelSource":{"format":"netascode-catalyst-center","modelVersion":"0.5.0","resolved":true,"exporter":"cvk-installed-credential-recovery"},"source":{"configMapRef":{"name":"placeholder","key":"model.json"}}}}`), &config); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.ConfigMapRef.Name = cm.Name
	if err := api.Create(ctx, &config); err != nil {
		t.Fatal(err)
	}
	configUID, sourceUID := config.UID, cm.UID
	poll := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("qualification wait expired; preserve checkpoint and original operation")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	read := func() {
		t.Helper()
		refresh()
		if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&config), &config); err != nil {
			t.Fatal(err)
		}
		if config.UID != configUID {
			t.Fatal("configuration replaced")
		}
		save("controller.json", &nc)
		save("config.json", &config)
	}
	poll(func() bool {
		read()
		return config.Status.ObservedGeneration == config.Generation && config.Status.Phase != ""
	})
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	tasks := []string{}
	unknowns := 0
	target := ""
	restarted := false
	var interruptedUID string
	for step := 0; step < 2; step++ {
		if step == 1 {
			if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
				t.Fatal(err)
			}
			if cm.UID != sourceUID {
				t.Fatal("source replaced")
			}
			cm.Data["model.json"] = string(noAuthCredentialModel(name, "cvkInstalledTwo"))
			if err := api.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		}
		ack, operation := "", ""
		seenUnknown := false
		poll(func() bool {
			read()
			state := nc.Status.Configuration
			if state == nil {
				return false
			}
			if op := state.Operation; op != nil {
				if op.ConfigUID != string(configUID) || op.SourceUID != string(sourceUID) || !isMetadataCredentialOperation(op.Section, op.Resource) {
					t.Fatal("foreign operation")
				}
				if validAPIID(op.TaskID) {
					if ack != "" && (op.TaskID != ack || op.ID != operation) {
						t.Fatal("original acknowledged operation replaced")
					}
					ack, operation = op.TaskID, op.ID
				}
				if op.Phase == "Unknown" {
					seenUnknown = true
					if step == 0 && !restarted {
						var pods core.PodList
						if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 {
							t.Fatal("sole worker required")
						}
						pod := pods.Items[0]
						if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image || pod.Spec.NodeName != "cvk-catc-qualification-20261017-control-plane" {
							t.Fatal("worker identity changed")
						}
						interruptedUID = string(pod.UID)
						save("interrupted-pod.json", &pod)
						save("interrupted-controller.json", &nc)
						if err := api.Delete(ctx, &pod, ctrlclient.Preconditions{UID: &pod.UID}); err != nil {
							t.Fatal(err)
						}
						restarted = true
					}
				}
				return false
			}
			if config.Status.ObservedGeneration != config.Generation || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				return false
			}
			var found *cvk.NetworkControllerConfigurationActionApplication
			for i := range state.ActionApplications {
				r := &state.ActionApplications[i]
				if r.Operation.ConfigUID == string(configUID) {
					if found != nil {
						t.Fatal("duplicate credential receipts")
					}
					found = r
				}
			}
			if found == nil || found.Operation.SourceVersion != cm.ResourceVersion {
				return false
			}
			if !seenUnknown || ack == "" || found.Operation.TaskID != ack || found.Operation.ID != operation {
				t.Fatal("original Unknown recovery not proven")
			}
			id, err := metadataCredentialReceiptTarget(found, time.Now())
			if err != nil || target != "" && id != target {
				t.Fatal("owned UUID changed", err)
			}
			target = id
			return true
		})
		unknowns++
		tasks = append(tasks, ack)
		save("phase-"+string(rune('0'+step))+"-controller.json", &nc)
	}
	if !restarted || unknowns != 2 || tasks[0] == tasks[1] {
		t.Fatal("missing installed recovery phases")
	}
	var pods core.PodList
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 || string(pods.Items[0].UID) == interruptedUID {
		t.Fatal("replacement worker not proven")
	}
	// Stop desired writes before independently supervised fixture cleanup.
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	poll(func() bool {
		read()
		return config.Status.ObservedGeneration == config.Generation && nc.Status.Configuration.Operation == nil
	})
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data["model.json"] = `{"catalyst_center":{"network_settings":{}}}`
	if err := api.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	actual, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := actual["snmpv3_credentials"][name]
	if !ok || got.ID != target || got.Username != "cvkInstalledTwo" || got.Mode != "NOAUTHNOPRIV" {
		t.Fatal("cleanup ownership drift")
	}
	observer.cleanup = target
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, globalCredentialsPath+"/"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
		t.Fatal("cleanup acknowledgement missing")
	}
	save("cleanup.json", map[string]any{"target": target, "task": ack.Response.ID, "submittedAt": submitted})
	poll(func() bool {
		done, err := c.configurationTaskTreeComplete(ctx, ack.Response.ID, submitted, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return done
	})
	after, err := c.globalCredentials(ctx)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("non-secret native baseline not restored", err)
	}
	poll(func() bool {
		read()
		return config.Spec.Mode == configapi.NetworkControllerApplyModeReport && config.Status.ObservedGeneration == config.Generation && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && nc.Status.Configuration.Operation == nil
	})
	for _, r := range baselineJournal.ActionApplications {
		found := false
		for _, current := range nc.Status.Configuration.ActionApplications {
			if reflect.DeepEqual(r, current) {
				found = true
			}
		}
		if !found {
			t.Fatal("prior site receipt changed")
		}
	}
	save("result.json", map[string]any{"installed": true, "unknownRecoveries": unknowns, "workerReplaced": restarted, "tasks": tasks, "target": target, "cleanupTask": ack.Response.ID, "nativeBaselineRestored": true, "priorSiteReceiptsUnchanged": true, "deviceAuthenticationTested": false})
	t.Log("installed credential Unknown create/update recovery and exact cleanup verified", tasks)
}

// A separate installed qualification reuses the settled metadata owner. It
// submits no credential mutation and retains all historical receipt identities.
func TestLiveInstalledEmptyCredentialSelection(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_EMPTY_CREDENTIALS") != "idle-retained-owner" {
		t.Skip("installed empty selection disabled")
	}
	path := os.Getenv("CATC_INSTALLED_KUBECONFIG")
	kc, err := clientcmd.LoadFromFile(path)
	if err != nil || !filepath.IsAbs(path) || kc.CurrentContext != "kind-cvk-catc-qualification-20261017" {
		t.Fatal("isolated kubeconfig required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var system core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || string(system.UID) != os.Getenv("CATC_INSTALLED_CLUSTER_UID") {
		t.Fatal("cluster identity changed")
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lifecycle"}
	var nc cvk.NetworkController
	if err := api.Get(ctx, key, &nc); err != nil || string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		t.Fatal("idle pinned controller required")
	}
	before := nc.Status.Configuration.DeepCopy()
	var config configapi.NetworkControllerConfig
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "installed-noauth-recovery"}, &config); err != nil || config.Spec.Mode != configapi.NetworkControllerApplyModeReport || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyRetain || len(config.Spec.SecretRefs) != 0 {
		t.Fatal("settled report fixture required")
	}
	owned := false
	for _, o := range before.Owners {
		if o.Section == "network_settings" && o.ConfigUID == string(config.UID) && o.ConfigName == config.Name {
			owned = true
		}
	}
	if !owned {
		t.Fatal("existing owner required")
	}
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	var deployment apps.Deployment
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261018-empty-credentials-") {
		t.Fatal("empty selection image required")
	}
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "catc-lifecycle-controller-worker"}, &deployment); err != nil || len(deployment.Spec.Template.Spec.Containers) != 1 || deployment.Spec.Template.Spec.Containers[0].Image != image || deployment.Status.AvailableReplicas != 1 {
		t.Fatal("pinned worker unavailable")
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-empty-credentials-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed empty selection evidence", dir)
	save := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := liveClient(t)
	c.mu.Lock()
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		return &siteRetainedReadOnlyTransport{base: base}
	}
	c.mu.Unlock()
	native, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cm core.ConfigMap
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: key.Namespace, Name: config.Spec.Source.ConfigMapRef.Name}, &cm); err != nil {
		t.Fatal(err)
	}
	uid, sourceUID := config.UID, cm.UID
	configKey := ctrlclient.ObjectKeyFromObject(&config)
	cmKey := ctrlclient.ObjectKeyFromObject(&cm)
	save("before-controller.json", &nc)
	save("before-config.json", &config)
	groups := map[string]any{}
	for _, kind := range globalCredentialKinds {
		groups[kind.model] = []any{}
	}
	for index, selection := range []map[string]any{groups, {}} {
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": selection}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := api.Get(ctx, cmKey, &cm); err != nil || cm.UID != sourceUID {
			t.Fatal("source identity changed")
		}
		cm.Data[config.Spec.Source.ConfigMapRef.Key] = string(raw)
		if err := api.Update(ctx, &cm); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []configapi.NetworkControllerApplyMode{configapi.NetworkControllerApplyModeApply, configapi.NetworkControllerApplyModeReport} {
			if err := api.Get(ctx, configKey, &config); err != nil || config.UID != uid {
				t.Fatal("config identity changed")
			}
			config.Spec.Mode = mode
			if err := api.Update(ctx, &config); err != nil {
				t.Fatal(err)
			}
			generation := config.Generation
			for {
				if err := api.Get(ctx, key, &nc); err != nil || !reflect.DeepEqual(before, nc.Status.Configuration) {
					t.Fatal("empty selection changed retained journal")
				}
				if err := api.Get(ctx, configKey, &config); err != nil || config.UID != uid || config.Generation != generation {
					t.Fatal("config revision changed")
				}
				save("controller.json", &nc)
				save("config.json", &config)
				if config.Status.ObservedGeneration == generation && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					if len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 0 {
						t.Fatal("nonzero empty selection")
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("empty selection not converged", config.Status.Conditions)
				case <-time.After(200 * time.Millisecond):
				}
			}
			t.Log("installed empty collection converged", index, mode, generation)
		}
	}
	after, err := c.globalCredentials(ctx)
	if err != nil || !reflect.DeepEqual(native, after) {
		t.Fatal("native credential metadata changed", err)
	}
	save("result.json", map[string]any{"installed": true, "collections": 6, "emptyObject": true, "applyAndReport": true, "retainedJournalUnchanged": true, "nativeMetadataUnchanged": true, "configUID": uid, "sourceUID": sourceUID, "terminalMode": config.Spec.Mode, "terminalPhase": config.Status.Phase})
}
