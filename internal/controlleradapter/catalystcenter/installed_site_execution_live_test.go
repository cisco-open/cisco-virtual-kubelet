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
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Uses an actual installed manager/worker, not an in-process reconciler or fake
// storage. An explicitly named private kind cluster is required; never falls
// back to the user's current kubeconfig. Failures retain intent and fixtures.
func TestLiveInstalledPrivateSiteExecution(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_SITE_EXECUTION") != "isolated-private-hierarchy" {
		t.Skip("explicit installed private-site qualification disabled")
	}
	path := os.Getenv("CATC_INSTALLED_KUBECONFIG")
	clusterUID, controllerUID := os.Getenv("CATC_INSTALLED_CLUSTER_UID"), os.Getenv("CATC_INSTALLED_CONTROLLER_UID")
	if !filepath.IsAbs(path) || clusterUID == "" || controllerUID == "" {
		t.Fatal("explicit private kubeconfig and immutable qualification identities required")
	}
	restartAccepted := os.Getenv("CATC_INSTALLED_ACCEPTED_RESTART")
	if restartAccepted != "" && restartAccepted != "update" {
		t.Fatal("unknown installed accepted-task restart qualification")
	}
	if restartAccepted != "" {
		if _, err := exec.LookPath("docker"); err != nil {
			t.Fatal("accepted-task process restart requires the private kind runtime")
		}
	}
	shape := os.Getenv("CATC_INSTALLED_SITE_SHAPE")
	if shape != "" && shape != "floor" {
		t.Fatal("unknown installed private-site shape")
	}
	imageMode := os.Getenv("CATC_INSTALLED_FLOOR_IMAGES")
	if imageMode != "" && (imageMode != "two-revisions" && imageMode != "two-revisions-and-path-replace" && imageMode != "omit-reintroduce" && imageMode != "format-lifetimes" || shape != "floor") {
		t.Fatal("installed floor-image mode requires explicit floor/two-revisions qualification")
	}
	retirementMode := os.Getenv("CATC_INSTALLED_SITE_RETIREMENT")
	if retirementMode != "" && (retirementMode != "observe-empty-floor" || shape != "floor") {
		t.Fatal("unknown installed retirement qualification; empty floor required")
	}
	wantKinds := []string{"area", "building"}
	if shape == "floor" {
		wantKinds = append(wantKinds, "floor")
	}
	creatorCount := len(wantKinds)
	kubeconfig, err := clientcmd.LoadFromFile(path)
	if err != nil || kubeconfig.CurrentContext != "kind-cvk-catc-qualification-20261017" {
		t.Fatal("requires dedicated qualification context")
	}
	rest, err := clientcmd.NewDefaultClientConfig(*kubeconfig, &clientcmd.ConfigOverrides{}).ClientConfig()
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
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lab"}
	configKey := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "installed-private-sites"}
	var nc cvk.NetworkController
	var config configapi.NetworkControllerConfig
	refresh := func() {
		t.Helper()
		if err := api.Get(ctx, key, &nc); err != nil {
			t.Fatal(err)
		}
		if string(nc.UID) != controllerUID || nc.Spec.Type != "catalyst-center" || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Spec.Paused || nc.DeletionTimestamp != nil {
			t.Fatal("qualification controller changed or paused")
		}
		if err := api.Get(ctx, configKey, &config); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	if config.Spec.Mode != configapi.NetworkControllerApplyModeReport || !siteComparisonConfig(&config) || config.Spec.PrunePolicy != "Retain" || config.Spec.DeletionPolicy != "Retain" || nc.Status.Configuration != nil {
		t.Fatal("requires original unclaimed Report-only private-site preparation")
	}
	if imageMode != "" && config.Spec.Source.ConfigMapRef == nil {
		t.Fatal("floor-image qualification requires the original ConfigMap source")
	}
	configUID := config.UID
	source, err := loadControllerSource(ctx, api, &config)
	if err != nil {
		t.Fatal(err)
	}
	desired, placements, initialImages, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil || len(desired) != creatorCount || parentSitePath(desired[0].path) != "Global" || !strings.HasPrefix(lastSiteName(desired[0].path), "CVK_INST_") {
		t.Fatal("requires exact generated empty hierarchy preparation", err)
	}
	if len(placements) != 0 || len(initialImages) != 0 {
		t.Fatal("qualification must start without image or AP declarations")
	}
	for i, kind := range wantKinds {
		if desired[i].kind != kind || i > 0 && parentSitePath(desired[i].path) != desired[i-1].path {
			t.Fatal("qualification hierarchy kind/parent changed")
		}
	}
	c := liveClient(t)
	checkEmptyFloor := func() {
		t.Helper()
		if shape != "floor" {
			return
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id := ""
		for _, site := range actual {
			if site.NameHierarchy == desired[2].path && site.Type == "floor" {
				id = site.ID
			}
		}
		if id == "" {
			t.Fatal("generated qualification floor missing")
		}
		positions, err := c.plannedAPNativePositions(ctx, id)
		if err != nil || len(positions) != 0 {
			t.Fatal("empty-floor qualification refuses planned AP members", err)
		}
		devices, err := c.assignedDevices(ctx, id)
		if err != nil || len(devices) != 0 {
			t.Fatal("empty-floor qualification refuses assigned devices", err)
		}
	}
	baseline, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range baseline {
		if site.NameHierarchy == desired[0].path || strings.HasPrefix(site.NameHierarchy, desired[0].path+"/") {
			t.Fatal("prepared generated hierarchy already exists; no adoption/replay")
		}
	}
	t.Log("installed private-site baseline absent", desired[0].path)
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("installed qualification did not settle; retain fixture")
			case <-time.After(time.Second):
			}
		}
	}
	settle := func(rows int) {
		t.Helper()
		wait(func() bool {
			refresh()
			if config.UID != configUID {
				t.Fatal("configuration replaced during qualification")
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed && config.Status.ObservedGeneration == config.Generation {
				for _, condition := range config.Status.Conditions {
					t.Log("installed refusal", condition.Type, condition.Reason)
				}
				t.Fatal("installed Apply refused; retain intent and exact hierarchy")
			}
			return config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && nc.Status.Configuration != nil && nc.Status.Configuration.Operation == nil && len(nc.Status.Configuration.ActionApplications) == rows
		})
		for _, row := range nc.Status.Configuration.ActionApplications {
			op := row.Operation
			if op.ConfigUID != string(configUID) || op.ConfigName != config.Name || op.SubmittedAt == nil {
				t.Fatal("foreign or incomplete installed receipt")
			}
			complete, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
			if err != nil || !complete {
				t.Fatal("installed original task tree not independently verified", err)
			}
			t.Log("installed verified original task", op.Action, op.TaskID)
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if next, err := nextSiteMutation(desired, actual); err != nil || next != nil {
			t.Fatal("installed source/native attributes differ", err)
		}
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	if err := api.Update(ctx, &config); err != nil {
		t.Fatal(err)
	}
	settle(creatorCount)
	creators, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil || len(creators) != creatorCount {
		t.Fatal("installed private creators missing", err)
	}
	checkEmptyFloor()
	for _, row := range creators {
		if row.keyID == "" || row.mac == "" {
			t.Fatal("installed creators are not privately authenticated")
		}
	}
	var model map[string]any
	if json.Unmarshal(source.raw, &model) != nil {
		t.Fatal("invalid original source")
	}
	sitesModel := model["catalyst_center"].(map[string]any)["sites"].(map[string]any)
	if shape == "floor" {
		sitesModel["floors"].([]any)[0].(map[string]any)["width"] = 12.0
	} else {
		sitesModel["buildings"].([]any)[0].(map[string]any)["latitude"] = 37.33822
	}
	updated, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	desired, err = parseSites(updated)
	if err != nil {
		t.Fatal(err)
	}
	if config.Spec.Source.Inline != nil {
		config.Spec.Source.Inline.Raw = updated
		if err := api.Update(ctx, &config); err != nil {
			t.Fatal(err)
		}
	} else {
		var cm core.ConfigMap
		if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: config.Spec.Source.ConfigMapRef.Name}, &cm); err != nil || cm.UID != source.uid {
			t.Fatal("original geometry ConfigMap changed", err)
		}
		cm.Data[config.Spec.Source.ConfigMapRef.Key] = string(updated)
		if err := api.Update(ctx, &cm); err != nil {
			t.Fatal(err)
		}
	}
	acceptedTask := ""
	if restartAccepted == "update" {
		wait(func() bool {
			refresh()
			op := nc.Status.Configuration.Operation
			if op == nil || op.Action != "Update" || !validAPIID(op.TaskID) || op.Phase != "Accepted" && op.Phase != "Verifying" {
				if len(nc.Status.Configuration.ActionApplications) > creatorCount {
					t.Fatal("accepted-task interruption window missed; no recovery proof claimed")
				}
				return false
			}
			acceptedTask = op.TaskID
			return true
		})
		var pods core.PodList
		if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lab-controller-worker"}); err != nil || len(pods.Items) != 1 {
			t.Fatal("accepted operation lacks sole installed worker", err)
		}
		pod := pods.Items[0]
		if pod.Spec.NodeName != "cvk-catc-qualification-20261017-control-plane" || len(pod.Status.ContainerStatuses) != 1 {
			t.Fatal("accepted-task restart is restricted to the exact private kind node/worker")
		}
		status := pod.Status.ContainerStatuses[0]
		id, ok := strings.CutPrefix(status.ContainerID, "containerd://")
		if !ok || len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" || status.Name != "controller-worker" {
			t.Fatal("installed worker runtime identity is not exact")
		}
		// Kill only this observed test worker process. Kubelet restarts the same
		// Pod without changing source/controller generations or mutation authority.
		if _, err := exec.CommandContext(ctx, "docker", "exec", pod.Spec.NodeName, "crictl", "stop", "--timeout", "0", id).Output(); err != nil {
			t.Fatal("private worker process interruption failed; retain fixture", err)
		}
		refresh()
		if nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.TaskID != acceptedTask || len(nc.Status.Configuration.ActionApplications) != creatorCount {
			t.Fatal("original task was already finalized at interruption; recovery window unproven")
		}
		wait(func() bool {
			var current core.Pod
			if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(&pod), &current); err != nil {
				t.Fatal(err)
			}
			if current.UID != pod.UID {
				t.Fatal("kubelet process restart unexpectedly replaced the Pod")
			}
			return len(current.Status.ContainerStatuses) == 1 && current.Status.ContainerStatuses[0].RestartCount > status.RestartCount && current.Status.ContainerStatuses[0].ContainerID != status.ContainerID
		})
		t.Log("installed worker process restarted with original acknowledged update still retained", acceptedTask)
	}
	settle(creatorCount + 1)
	checkEmptyFloor()
	after, err := siteCreatedRecords(nc.Status.Configuration)
	latest, latestErr := siteAppliedRecords(nc.Status.Configuration)
	if err != nil || latestErr != nil || !reflect.DeepEqual(creators, after) || len(latest) != 1 {
		t.Fatal("installed update replaced creators or omitted latest-applied proof", err, latestErr)
	}
	if acceptedTask != "" {
		for _, row := range latest {
			if row.Operation.TaskID != acceptedTask {
				t.Fatal("restarted worker replaced the original acknowledged task")
			}
		}
		t.Log("real restarted worker recovered original acknowledged update through full task tree and private latest-applied publication", acceptedTask)
	}
	if imageMode != "" {
		qualifyInstalledFloorImages(t, ctx, api, c, &config, &nc, refresh, wait)
		checkEmptyFloor()
	}
	if retirementMode != "" {
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var target Site
		for _, site := range actual {
			if site.NameHierarchy == desired[2].path {
				target = site
			}
		}
		if err := c.stableUnusedSiteLeaf(ctx, target); err != nil {
			t.Fatal("generated empty floor retirement dependencies unavailable or present; retain fixture", err)
		}
		t.Log("two complete independent empty-floor retirement dependency/native observations passed; this is not public deletion authority")
	}
	journal := nc.Status.Configuration.DeepCopy()
	if config.Status.LastControllerCheck == nil {
		t.Fatal("installed controller check timestamp missing")
	}
	checked := config.Status.LastControllerCheck.DeepCopy()
	var pods core.PodList
	if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lab-controller-worker"}); err != nil || len(pods.Items) != 1 {
		t.Fatal("requires sole current installed worker", err)
	}
	oldPod := pods.Items[0]
	if err := api.Delete(ctx, &oldPod, ctrlclient.Preconditions{UID: &oldPod.UID}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		refresh()
		if nc.Status.Configuration == nil || !reflect.DeepEqual(journal.ActionApplications, nc.Status.Configuration.ActionApplications) || nc.Status.Configuration.Operation != nil {
			t.Fatal("worker replacement changed receipts or created new work")
		}
		if err := api.List(ctx, &pods, ctrlclient.InNamespace(key.Namespace), ctrlclient.MatchingLabels{"app.kubernetes.io/instance": "catc-lab-controller-worker"}); err != nil {
			t.Fatal(err)
		}
		return len(pods.Items) == 1 && pods.Items[0].UID != oldPod.UID && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.LastControllerCheck != nil && config.Status.LastControllerCheck.After(checked.Time)
	})
	t.Logf("real worker replacement retained %d private creators and one latest-applied receipt; source/native readback and refreshed InSync verified", creatorCount)

	// Retain deletion must retire intent before independent supervised cleanup;
	// public site prune/delete is deliberately not inferred from this cleanup.
	if err := api.Delete(ctx, &config, ctrlclient.Preconditions{UID: &configUID}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		var current configapi.NetworkControllerConfig
		err := api.Get(ctx, configKey, &current)
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
		return apierrors.IsNotFound(err)
	})
	if err := api.Get(ctx, key, &nc); err != nil || nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		t.Fatal("retired intent retained uncertain operation", err)
	}
	cleanupInstalledPrivateSites(t, ctx, c, desired, creators, checkEmptyFloor)
	actual, err := c.ListSites(ctx)
	if err != nil || !reflect.DeepEqual(baseline, actual) {
		t.Fatal("unrelated native site baseline not restored", err)
	}
	t.Log("installed private Create/Update, real Pod replacement, Retain intent retirement and supervised exact cleanup passed; all site baselines restored; no physical operation")
}

// Shared by normal completion and explicit retained-fixture recovery.
func cleanupInstalledPrivateSites(t *testing.T, ctx context.Context, c *client, desired []desiredSite, creators map[string]siteCreatorRecord, checkEmptyFloor func()) {
	t.Helper()
	for i := len(desired) - 1; i >= 0; i-- {
		path := desired[i].path
		if desired[i].kind == "floor" {
			checkEmptyFloor()
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var target Site
		for _, site := range actual {
			if site.NameHierarchy == path {
				target = site
			}
			if strings.HasPrefix(site.NameHierarchy, path+"/") {
				t.Fatal("supervised cleanup refused remaining child")
			}
		}
		owned := false
		for _, row := range creators {
			owned = owned || row.id == target.ID && row.kind == target.Type
		}
		if !owned || target.ID == "" {
			t.Fatal("supervised cleanup identity replaced/missing")
		}
		reader := c.profileSitePruneReader()
		if err := reader.loadSites(ctx); err != nil {
			t.Fatal(err)
		}
		if err := reader.unusedScope(ctx, path); err != nil {
			t.Fatal("supervised cleanup consumer preflight refused", err)
		}
		endpoint := "/dna/intent/api/v1/areas/"
		if target.Type == "building" {
			endpoint = "/dna/intent/api/v2/buildings/"
		}
		if target.Type == "floor" {
			endpoint = "/dna/intent/api/v2/floors/"
		}
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, endpoint+target.ID, nil)
		if err != nil {
			t.Fatal("supervised cleanup outcome uncertain; no replay", err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("supervised cleanup lacks acknowledged task; no replay")
		}
		if err := waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
			t.Fatal("supervised cleanup original task uncertain", err)
		}
		actual, err = c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, site := range actual {
			if site.ID == target.ID || site.NameHierarchy == path {
				t.Fatal("supervised cleanup absence unverified")
			}
		}
		t.Log("supervised generated-site cleanup verified", target.Type, receipt.Response.ID)
	}
}
