//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The test only changes the generated source/configuration and restarts the
// exact isolated worker. All appliance writes are made by the installed worker.
// It never reads a Secret or copies the projected comparison key to the host.
func TestLiveInstalledSiteLifecycle(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_SITE_LIFECYCLE") != "isolated-area-building-floor" {
		t.Skip("explicit installed site lifecycle qualification disabled")
	}
	path := os.Getenv("CATC_INSTALLED_KUBECONFIG")
	controllerUID := os.Getenv("CATC_INSTALLED_CONTROLLER_UID")
	clusterUID := os.Getenv("CATC_INSTALLED_CLUSTER_UID")
	if !filepath.IsAbs(path) || controllerUID == "" || clusterUID == "" {
		t.Fatal("explicit private kubeconfig and immutable qualification identities required")
	}
	kc, err := clientcmd.LoadFromFile(path)
	if err != nil || kc.CurrentContext != "kind-cvk-catc-qualification-20261017" {
		t.Fatal("dedicated qualification context required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var system core.Namespace
	if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "kube-system"}, &system); err != nil || string(system.UID) != clusterUID {
		t.Fatal("qualification cluster identity changed")
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lifecycle"}
	var nc cvk.NetworkController
	refreshController := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil {
			t.Fatal(err)
		}
		if string(nc.UID) != controllerUID || nc.DeletionTimestamp != nil || nc.Spec.Paused || nc.Spec.Type != "catalyst-center" || nc.Spec.Endpoint != "https://host.docker.internal:19443" {
			t.Fatal("qualification controller identity changed")
		}
	}
	refreshController()
	if nc.Status.Configuration != nil {
		t.Fatal("fresh unclaimed qualification controller required; never resume by replaying this test")
	}
	var worker apps.Deployment
	image := os.Getenv("CATC_INSTALLED_IMAGE")
	if !strings.HasPrefix(image, "cvk-catc-worker-qualification:20261017-site-lifecycle-") {
		t.Fatal("exact source-pinned qualification image required")
	}
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "catc-lifecycle-controller-worker"}, &worker); err != nil || len(worker.Spec.Template.Spec.Containers) != 1 || worker.Spec.Template.Spec.Containers[0].Image != image || worker.Status.AvailableReplicas != 1 {
		t.Fatal("source-pinned installed worker is not available", err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-installed-site-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private installed lifecycle evidence:", dir)
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
	c := liveClient(t)
	c.mu.Lock()
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		return &siteRetainedReadOnlyTransport{base: base}
	}
	c.mu.Unlock()
	baseline, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	save("baseline-sites.json", baseline)
	name := "CVK_INST_LIFE_" + time.Now().UTC().Format("0102_150405")
	root := "Global/" + name
	desired := []desiredSite{{kind: "area", path: root, fields: map[string]any{}}, {kind: "building", path: root + "/Building", fields: map[string]any{"country": "United States", "latitude": 37.33812, "longitude": -121.88612}}, {kind: "floor", path: root + "/Building/Floor", fields: map[string]any{"floor_number": float64(1), "rf_model": "Free Space", "width": float64(10), "length": float64(15), "height": float64(3), "units_of_measure": "meters"}}}
	for _, s := range baseline {
		if s.NameHierarchy == root || strings.HasPrefix(s.NameHierarchy, root+"/") {
			t.Fatal("generated hierarchy already exists; no adoption")
		}
	}
	raw, err := siteManifestModel(desired)
	if err != nil {
		t.Fatal(err)
	}
	cm := core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "installed-lifecycle-model-" + strings.ToLower(time.Now().UTC().Format("150405")), Namespace: key.Namespace}, Data: map[string]string{"model.json": string(raw)}}
	if err := api.Create(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := json.Unmarshal([]byte(`{"apiVersion":"config.cisco.vk/v1alpha1","kind":"NetworkControllerConfig","metadata":{"name":"installed-site-lifecycle","namespace":"cvk-catc-qualification","annotations":{"cisco.vk/catalyst-center-bulk-sites":"false"}},"spec":{"controllerRef":{"name":"catc-lifecycle"},"managedSections":["sites"],"scope":"global","mode":"report","prunePolicy":"Delete","deletionPolicy":"Retain","driftDetectInterval":"30s","taskTimeout":"10m","secretRefs":[{"path":"/sites","section":"sites","source":"site-state-key","use":"PrivateComparisonKey"}],"modelSource":{"format":"netascode-catalyst-center","modelVersion":"0.5.0","resolved":true,"exporter":"cvk-installed-site-lifecycle"},"source":{"configMapRef":{"name":"placeholder","key":"model.json"}}}}`), &config); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.ConfigMapRef.Name = cm.Name
	if err := api.Create(ctx, &config); err != nil {
		t.Fatal(err)
	}
	configUID, sourceUID := config.UID, cm.UID
	var applyGeneration int64
	refresh := func() {
		t.Helper()
		refreshController()
		if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&config), &config); err != nil {
			t.Fatal(err)
		}
		if config.UID != configUID || applyGeneration != 0 && config.Generation != applyGeneration {
			t.Fatal("configuration identity/generation changed")
		}
		save("controller.json", &nc)
		save("config.json", &config)
	}
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("installed lifecycle wait expired; retain exact intent and original tasks")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var unexpected configapi.NetworkControllerConfig
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "installed-private-sites"}, &unexpected); !apierrors.IsNotFound(err) {
		t.Fatal("other qualification configuration still exists")
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	applyGeneration = config.Generation
	settle := func(label string, rows int, want []desiredSite) {
		t.Helper()
		wait(func() bool {
			refresh()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed && config.Status.ObservedGeneration == config.Generation {
				for _, condition := range config.Status.Conditions {
					t.Log("installed refusal", condition.Reason)
				}
				t.Fatal("installed lifecycle refused; preserve fixture")
			}
			return config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && nc.Status.Configuration != nil && nc.Status.Configuration.Operation == nil && len(nc.Status.Configuration.ActionApplications) == rows
		})
		state := nc.Status.Configuration
		if len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(configUID) {
			t.Fatal("installed lifecycle lost sole owner")
		}
		for _, row := range state.ActionApplications {
			op := row.Operation
			if op.ConfigUID != string(configUID) || op.ConfigGeneration != applyGeneration || op.SourceUID != string(sourceUID) || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
				t.Fatal("incomplete installed task/source binding")
			}
			done, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
			if err != nil || !done {
				t.Fatal("installed original task tree incomplete", err)
			}
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if next, err := nextSiteMutation(want, actual); err != nil || next != nil {
			t.Fatal("installed native desired-state mismatch", err)
		}
		if len(want) == 0 {
			for _, site := range actual {
				if site.NameHierarchy == root || strings.HasPrefix(site.NameHierarchy, root+"/") {
					t.Fatal("retired hierarchy still present")
				}
			}
		}
		save(label+"-controller.json", &nc)
		save(label+"-config.json", &config)
		t.Log("installed lifecycle settled", label, "retained rows", rows)
	}
	update := func(want []desiredSite) {
		t.Helper()
		if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil || cm.UID != sourceUID {
			t.Fatal("original ConfigMap replaced")
		}
		raw, err := siteManifestModel(want)
		if err != nil {
			t.Fatal(err)
		}
		cm.Data["model.json"] = string(raw)
		if err := api.Update(ctx, &cm); err != nil {
			t.Fatal(err)
		}
		save("source.json", &cm)
	}
	settle("created", 3, desired)
	originals, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil || len(originals) != 3 {
		t.Fatal("installed private creators unavailable", err)
	}
	for _, r := range originals {
		if r.keyID == "" || r.mac == "" {
			t.Fatal("installed worker did not publish private creator commitments")
		}
	}
	// Capture and interrupt the first acknowledged floor Delete. No host-side
	// operation/phase changes or direct appliance mutation are used for recovery.
	update(nil)
	ack := ""
	var interrupted core.Pod
	var prior core.ContainerStatus
	wait(func() bool {
		refresh()
		state := nc.Status.Configuration
		if state == nil {
			return false
		}
		op := state.Operation
		if op == nil || op.Action != "Delete" || op.Phase != "Accepted" && op.Phase != "Verifying" || !validAPIID(op.TaskID) {
			if len(state.ActionApplications) > 3 {
				t.Fatal("acknowledged retirement interruption window missed; do not infer recovery")
			}
			return false
		}
		if op.ConfigUID != string(configUID) || op.TargetID == "" {
			t.Fatal("unexpected retirement identity")
		}
		floor, found := siteCreatorForResource(originals, desired[2].path)
		if !found || op.TargetID != floor.id {
			t.Fatal("retirement did not start at owned floor")
		}
		ack = op.TaskID
		return true
	})
	var pods core.PodList
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 {
		t.Fatal("sole isolated worker required", err)
	}
	interrupted = pods.Items[0]
	if interrupted.Spec.NodeName != "cvk-catc-qualification-20261017-control-plane" || len(interrupted.Status.ContainerStatuses) != 1 {
		t.Fatal("unexpected isolated worker runtime")
	}
	prior = interrupted.Status.ContainerStatuses[0]
	id, ok := strings.CutPrefix(prior.ContainerID, "containerd://")
	if !ok || len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" || prior.Name != "controller-worker" {
		t.Fatal("invalid exact worker process identity")
	}
	if _, err := exec.CommandContext(ctx, "docker", "exec", interrupted.Spec.NodeName, "crictl", "stop", "--timeout", "0", id).Output(); err != nil {
		t.Fatal("isolated worker interruption failed", err)
	}
	refresh()
	if nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.TaskID != ack || len(nc.Status.Configuration.ActionApplications) != 3 {
		t.Fatal("retirement settled before interruption; recovery window unproven")
	}
	save("interrupted-controller.json", &nc)
	wait(func() bool {
		var p core.Pod
		if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&interrupted), &p); err != nil {
			t.Fatal(err)
		}
		if p.UID != interrupted.UID {
			t.Fatal("worker process restart replaced Pod")
		}
		return len(p.Status.ContainerStatuses) == 1 && p.Status.ContainerStatuses[0].RestartCount > prior.RestartCount && p.Status.ContainerStatuses[0].ContainerID != prior.ContainerID
	})
	settle("retired", 6, nil)
	foundAck := false
	for _, row := range nc.Status.Configuration.ActionApplications {
		if row.Operation.TaskID == ack && row.Operation.Action == "Delete" {
			foundAck = true
		}
	}
	if !foundAck {
		t.Fatal("restarted worker replaced acknowledged retirement task")
	}
	t.Log("installed worker recovered original acknowledged floor retirement", ack)
	update(desired)
	settle("recreated", 9, desired)
	replacements, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil || len(replacements) != 3 {
		t.Fatal("replacement creators missing", err)
	}
	for i, want := range desired {
		old, _ := siteCreatorForResource(originals, want.path)
		next, found := siteCreatorForResource(replacements, want.path)
		if !found || old.id == next.id {
			t.Fatal("retired UUID reused")
		}
		if i > 0 {
			parent, _ := siteCreatorForResource(replacements, desired[i-1].path)
			if next.receipt.Operation.ParentID != parent.id || !strings.HasPrefix(next.receipt.Operation.Resource, siteRecreateHandoffPrefix) {
				t.Fatal("installed replacement parent handoff missing")
			}
		}
	}
	update(nil)
	settle("cleaned", 12, nil)
	state := nc.Status.Configuration.DeepCopy()
	archives, err := siteHistoryCohorts(state)
	if err != nil || len(archives) != 3 {
		t.Fatal("installed predecessor history incomplete", err)
	}
	tasks := map[string]bool{}
	creates, deletes := 0, 0
	for _, row := range state.ActionApplications {
		op := row.Operation
		if tasks[op.TaskID] {
			t.Fatal("duplicate native task across retained receipt slots")
		}
		tasks[op.TaskID] = true
		switch op.Action {
		case "Create":
			creates++
		case "Delete":
			deletes++
		default:
			t.Fatal("unexpected installed native action")
		}
	}
	if creates != 6 || deletes != 6 {
		t.Fatal("installed lifecycle action count mismatch")
	}
	final, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sortSites := func(s []Site) { sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID }) }
	sortSites(final)
	sortSites(baseline)
	if !reflect.DeepEqual(final, baseline) {
		t.Fatal("typed inventory baseline not restored")
	}
	if config.Status.LastControllerCheck == nil {
		t.Fatal("missing terminal native check")
	}
	checked := config.Status.LastControllerCheck.DeepCopy()
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil || len(pods.Items) != 1 {
		t.Fatal("sole terminal worker required", err)
	}
	oldPod := pods.Items[0]
	if err := api.Delete(ctx, &oldPod, ctrlclient.Preconditions{UID: &oldPod.UID}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		refresh()
		if !reflect.DeepEqual(state, nc.Status.Configuration) {
			t.Fatal("terminal worker replacement changed retained journal")
		}
		if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lifecycle-controller-worker"}); err != nil {
			t.Fatal(err)
		}
		return len(pods.Items) == 1 && pods.Items[0].UID != oldPod.UID && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.LastControllerCheck != nil && config.Status.LastControllerCheck.After(checked.Time)
	})
	save("result.json", map[string]any{"installed": true, "baselineRestored": true, "creates": creates, "deletes": deletes, "tasks": len(tasks), "archivedCohorts": len(archives), "receiptRows": len(state.ActionApplications), "controllerUID": controllerUID, "configUID": configUID, "sourceUID": sourceUID, "configGeneration": applyGeneration, "retirementTask": ack, "workerProcessRestart": true, "terminalWorkerPodReplaced": true, "keyReadOnHost": false})
	t.Log("installed three-kind lifecycle passed: six Creates/six Deletes, original retirement task recovered after worker interruption, replacement parent handoffs, retained history, terminal restart no replay and typed baseline restored")
}
