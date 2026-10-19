//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

type installedVersionedObserver struct {
	base    http.RoundTripper
	cleanup map[string]bool
}

func (o *installedVersionedObserver) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.URL.Path != authPath {
		if r.Method != http.MethodDelete || !o.cleanup[r.URL.Path] {
			return nil, errors.New("installed versioned qualification host mutation refused")
		}
		delete(o.cleanup, r.URL.Path)
	}
	return o.base.RoundTrip(r)
}

// Only generated, unassigned definitions and generated Secret keys are used.
// Secret API reads, projected-file reads and journal phase edits are forbidden.
func TestLiveInstalledVersionedCredentialRecovery(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_VERSIONED_CREDENTIALS") != "isolated-generated-six-types" {
		t.Skip("installed versioned qualification disabled")
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
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	var system core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || string(system.UID) != os.Getenv("CATC_INSTALLED_CLUSTER_UID") {
		t.Fatal("isolated cluster identity changed")
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lifecycle"}
	var nc cvk.NetworkController
	refresh := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil || string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.DeletionTimestamp != nil || nc.Spec.Paused {
			t.Fatal("pinned controller identity changed")
		}
	}
	refresh()
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.CredentialApplications) != 0 {
		t.Fatal("idle owner without versioned receipts required; never replay partial test")
	}
	beforeJournal := nc.Status.Configuration.DeepCopy()
	originalSources := append([]cvk.NetworkControllerIntentSecretSource(nil), nc.Spec.IntentSecretSources...)
	var devices cvk.CiscoDeviceList
	if err := api.List(ctx, &devices, ctrlclient.InNamespace(key.Namespace)); err != nil || len(devices.Items) != 0 {
		t.Fatal("device-free qualification namespace required")
	}
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261018-empty-credentials-") {
		t.Fatal("source-pinned current image required")
	}
	depKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "catc-lifecycle-controller-worker"}
	var deployment apps.Deployment
	if err := api.Get(ctx, depKey, &deployment); err != nil || len(deployment.Spec.Template.Spec.Containers) != 1 || deployment.Spec.Template.Spec.Containers[0].Image != image || deployment.Status.AvailableReplicas != 1 {
		t.Fatal("pinned worker unavailable")
	}
	var config configapi.NetworkControllerConfig
	configKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "installed-noauth-recovery"}
	if err := api.Get(ctx, configKey, &config); err != nil || config.Spec.Mode != configapi.NetworkControllerApplyModeReport || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(config.Spec.SecretRefs) != 0 || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyRetain || config.Spec.TaskTimeout == nil || config.Spec.TaskTimeout.Duration != time.Nanosecond {
		t.Fatal("idle retained report config required")
	}
	owned := false
	for _, o := range beforeJournal.Owners {
		if o.Section == "network_settings" && o.ConfigUID == string(config.UID) && o.ConfigName == config.Name {
			owned = true
		}
	}
	if !owned {
		t.Fatal("existing network settings owner required")
	}
	configUID := config.UID
	var cm core.ConfigMap
	cmKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: config.Spec.Source.ConfigMapRef.Name}
	if err := api.Get(ctx, cmKey, &cm); err != nil {
		t.Fatal(err)
	}
	sourceUID := cm.UID
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-versioned-credentials-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed versioned evidence", dir)
	values := []string{}
	save := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range values {
			if bytes.Contains(raw, []byte(v)) {
				t.Fatal("generated credential value in checkpoint refused")
			}
		}
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	poll := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("installed versioned wait expired; retain exact sources and original tasks")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	read := func() {
		t.Helper()
		refresh()
		if err := api.Get(ctx, configKey, &config); err != nil || config.UID != configUID {
			t.Fatal("configuration replaced")
		}
		save("controller.json", &nc)
		save("config.json", &config)
	}
	save("before-controller.json", &nc)
	save("before-config.json", &config)
	c := liveClient(t)
	observer := &installedVersionedObserver{cleanup: map[string]bool{}}
	c.mu.Lock()
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		observer.base = base
		return observer
	}
	c.mu.Unlock()
	nativeBaseline, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	random := func() string {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			t.Fatal("cannot generate fixture value")
		}
		return hex.EncodeToString(b)
	}
	nonce := random()[:12]
	groups := map[string]any{}
	names := map[string]string{}
	for _, kind := range globalCredentialKinds {
		var entry map[string]any
		if json.Unmarshal([]byte(globalCredentialFixtureEntries[kind.model]), &entry) != nil {
			t.Fatal("invalid fixture")
		}
		name := "CVK_INST_" + nonce + "_" + kind.model
		entry["name"] = name
		groups[kind.model] = []any{entry}
		names[kind.model] = name
		if _, exists := nativeBaseline[kind.model][name]; exists {
			t.Fatal("generated native name occupied")
		}
	}
	model := func(version int64) []byte {
		for _, rows := range groups {
			entry := rows.([]any)[0].(map[string]any)
			for k := range entry {
				if strings.HasSuffix(k, "_version") {
					entry[k] = version
				}
			}
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": groups}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	initialModel := model(1)
	wants, err := parseGlobalCredentials(initialModel, true)
	if err != nil || len(wants) != 6 {
		t.Fatal("invalid six-family fixture", err)
	}
	secret := core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cvk-generated-" + nonce, Namespace: key.Namespace}, Type: core.SecretTypeOpaque, Data: map[string][]byte{}}
	refs := []configapi.NetworkControllerSecretRef{}
	sources := append([]cvk.NetworkControllerIntentSecretSource(nil), originalSources...)
	for _, want := range wants {
		for _, leaf := range want.credentials {
			alias := fmt.Sprintf("cvk-generated-%s-%d", nonce, len(refs))
			value := random()
			values = append(values, value)
			secret.Data[alias] = versionedSecretFixture(t, 1, value)
			refs = append(refs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: leaf.path, Source: alias})
			sources = append(sources, cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: secret.Name, Key: alias})
		}
	}
	// Create/update only the generated Secret from memory. Never Get/List Secrets.
	if err := api.Create(ctx, &secret); err != nil {
		t.Fatal("cannot create generated projection Secret")
	}
	save("generated-secret-identity.json", map[string]any{"name": secret.Name, "uid": secret.UID, "keys": len(secret.Data)})
	nc.Spec.IntentSecretSources = sources
	if err := api.Update(ctx, &nc); err != nil {
		t.Fatal(err)
	}
	// Projection selection needs both administrator aliases and config refs.
	if err := api.Get(ctx, configKey, &config); err != nil {
		t.Fatal(err)
	}
	config.Spec.SecretRefs = refs
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	workerProjectionReady := func(required bool) bool {
		if err := api.Get(ctx, depKey, &deployment); err != nil {
			t.Fatal(err)
		}
		if deployment.Status.ObservedGeneration != deployment.Generation || deployment.Status.AvailableReplicas != 1 || deployment.Status.UpdatedReplicas != 1 {
			return false
		}
		var current core.PodList
		if err := api.List(ctx, &current, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil {
			t.Fatal(err)
		}
		if len(current.Items) != 1 {
			return false
		}
		pod := current.Items[0]
		if pod.DeletionTimestamp != nil || len(pod.Status.ContainerStatuses) != 1 || !pod.Status.ContainerStatuses[0].Ready || len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image {
			return false
		}
		present := false
		for _, v := range pod.Spec.Volumes {
			if v.Projected != nil {
				for _, src := range v.Projected.Sources {
					if src.Secret != nil && src.Secret.Name == secret.Name {
						present = true
					}
				}
			}
		}
		return present == required
	}
	poll(func() bool { return workerProjectionReady(true) })
	if err := api.Get(ctx, cmKey, &cm); err != nil || cm.UID != sourceUID {
		t.Fatal("source replaced")
	}
	cm.Data[config.Spec.Source.ConfigMapRef.Key] = string(initialModel)
	if err := api.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	if err := api.Get(ctx, configKey, &config); err != nil {
		t.Fatal(err)
	}
	config.Spec.SecretRefs = refs
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	type retained struct{ ID, Task string }
	histories := []map[string]retained{}
	targets := map[string]string{}
	restarted := false
	interruptedPod := ""
	for stage := 0; stage < 2; stage++ {
		version := int64(stage + 1)
		if stage == 1 {
			for k := range secret.Data {
				value := random()
				values = append(values, value)
				secret.Data[k] = versionedSecretFixture(t, version, value)
			}
			if err := api.Update(ctx, &secret); err != nil {
				t.Fatal("generated Secret rotation update failed")
			}
			save("rotation-secret-identity.json", map[string]any{"name": secret.Name, "uid": secret.UID, "resourceVersion": secret.ResourceVersion})
			if err := api.Get(ctx, cmKey, &cm); err != nil || cm.UID != sourceUID {
				t.Fatal("source replaced")
			}
			cm.Data[config.Spec.Source.ConfigMapRef.Key] = string(model(version))
			if err := api.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		}
		seen := map[string]retained{}
		poll(func() bool {
			read()
			state := nc.Status.Configuration
			if state == nil {
				t.Fatal("journal lost")
			}
			if op := state.Operation; op != nil {
				if op.ConfigUID != string(configUID) || op.SourceUID != string(sourceUID) || !isGlobalCredentialResource(op.Section, op.Resource) {
					t.Fatal("foreign operation during versioned qualification")
				}
				if prior, exists := seen[op.Resource]; exists && (prior.ID != op.ID || prior.Task != op.TaskID) {
					t.Fatal("original acknowledged operation replaced")
				}
				if op.Phase == "Unknown" {
					if !validAPIID(op.TaskID) || len(op.CredentialVersions) == 0 {
						t.Fatal("missing acknowledged projection bindings")
					}
					seen[op.Resource] = retained{op.ID, op.TaskID}
					if stage == 0 && !restarted {
						var pods core.PodList
						if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 {
							t.Fatal("sole projection worker required")
						}
						pod := pods.Items[0]
						if pod.Spec.NodeName != "cvk-catc-qualification-20261017-control-plane" || len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != image {
							t.Fatal("worker identity changed")
						}
						interruptedPod = string(pod.UID)
						save("interrupted-controller.json", &nc)
						save("interrupted-pod.json", &pod)
						if err := api.Delete(ctx, &pod, ctrlclient.Preconditions{UID: &pod.UID}); err != nil {
							t.Fatal(err)
						}
						restarted = true
					}
				}
				return false
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Status.ObservedGeneration != config.Generation || len(seen) != 6 {
				return false
			}
			count := 0
			for _, r := range state.CredentialApplications {
				if r.ConfigUID != string(configUID) {
					t.Fatal("foreign versioned receipt")
				}
				original, ok := seen[r.Resource]
				if !ok || r.OperationID != original.ID || !validAPIID(r.TargetID) {
					t.Fatal("receipt lost original operation")
				}
				for _, v := range r.Versions {
					if v.Version != version || v.SecretName != secret.Name {
						return false
					}
				}
				if stage == 0 {
					targets[r.Resource] = r.TargetID
				} else if targets[r.Resource] != r.TargetID {
					t.Fatal("rotation changed UUID")
				}
				count++
			}
			return count == 6
		})
		histories = append(histories, seen)
		save(fmt.Sprintf("stage-%d-controller.json", stage), &nc)
		save(fmt.Sprintf("stage-%d-tasks.json", stage), seen)
		t.Log("installed versioned stage verified", stage, "original tasks", len(seen))
	}
	if !restarted {
		t.Fatal("installed projection recovery interruption not proven")
	}
	var pods core.PodList
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 || string(pods.Items[0].UID) == interruptedPod {
		t.Fatal("replacement worker not proven")
	}
	// End desired mutation before any independently supervised fixture deletion.
	if err := api.Get(ctx, configKey, &config); err != nil {
		t.Fatal(err)
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	config.Spec.SecretRefs = nil
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	if err := api.Get(ctx, cmKey, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data[config.Spec.Source.ConfigMapRef.Key] = string(emptyCredentialModel(`{}`))
	if err := api.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	poll(func() bool {
		read()
		return config.Status.ObservedGeneration == config.Generation && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && nc.Status.Configuration.Operation == nil
	})
	current, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range wants {
		got, ok := current[want.kind.model][want.metadata.Name]
		if !ok || !globalCredentialMatches(want.metadata, got) || targets[want.resource()] != got.ID {
			t.Fatal("generated cleanup identity drift")
		}
		observer.cleanup[globalCredentialsPath+"/"+got.ID] = true
	}
	cleanups := map[string]string{}
	for _, want := range wants {
		id := targets[want.resource()]
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, globalCredentialsPath+"/"+id, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			t.Fatal("cleanup acknowledgement unavailable")
		}
		cleanups[id] = ack.Response.ID
		save("cleanup-tasks.json", cleanups)
		poll(func() bool {
			done, err := c.configurationTaskTreeComplete(ctx, ack.Response.ID, submitted, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			return done
		})
	}
	after, err := c.globalCredentials(ctx)
	if err != nil || !reflect.DeepEqual(nativeBaseline, after) {
		t.Fatal("non-secret credential baseline not restored", err)
	}
	refresh()
	if nc.Status.Configuration.Operation != nil {
		t.Fatal("journal not idle after cleanup")
	}
	for _, r := range beforeJournal.ActionApplications {
		found := false
		for _, got := range nc.Status.Configuration.ActionApplications {
			if reflect.DeepEqual(r, got) {
				found = true
			}
		}
		if !found {
			t.Fatal("pre-existing receipt changed")
		}
	}
	nc.Spec.IntentSecretSources = originalSources
	if err := api.Update(ctx, &nc); err != nil {
		t.Fatal(err)
	}
	poll(func() bool { return workerProjectionReady(false) })
	if err := api.Delete(ctx, &secret, ctrlclient.Preconditions{UID: &secret.UID}); err != nil {
		t.Fatal("cannot delete exact generated projection Secret")
	}
	read()
	save("result.json", map[string]any{"installed": true, "families": 6, "projectedKeys": len(refs), "unknownRecoveries": 12, "sameUUIDRotation": true, "workerReplaced": restarted, "nativeBaselineRestored": true, "priorReceiptsUnchanged": true, "secretAPIReads": 0, "generatedSecretRemoved": true, "allowlistRestored": true, "deviceAuthenticationTested": false, "tasks": histories})
	t.Log("installed six-family projection/create/rotation recovery and supervised cleanup verified")
}
