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
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Explicit recovery of a settled, retained generated fixture. This is independent
// supervised cleanup, never public source pruning or automatic failure cleanup.
func TestLiveInstalledRetainedSiteCleanup(t *testing.T) {
	if os.Getenv("CATC_INSTALLED_RETAINED_CLEANUP") != "settled-generated-hierarchy" {
		t.Skip("explicit retained fixture cleanup disabled")
	}
	path, snapshot := os.Getenv("CATC_INSTALLED_KUBECONFIG"), os.Getenv("CATC_INSTALLED_RETAINED_SNAPSHOT")
	if !filepath.IsAbs(path) || !filepath.IsAbs(snapshot) {
		t.Fatal("explicit private kubeconfig and saved controller snapshot required")
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
	for _, add := range []func(*runtime.Scheme) error{core.AddToScheme, cvk.AddToScheme, configapi.AddToScheme} {
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
	var saved, nc cvk.NetworkController
	raw, err := os.ReadFile(snapshot)
	if err != nil || json.Unmarshal(raw, &saved) != nil {
		t.Fatal("saved settled controller required")
	}
	key := ctrlclient.ObjectKey{Namespace: "cvk-catc-qualification", Name: "catc-lab"}
	if err := api.Get(ctx, key, &nc); err != nil {
		t.Fatal(err)
	}
	if string(nc.UID) != os.Getenv("CATC_INSTALLED_CONTROLLER_UID") || nc.UID != saved.UID || nc.DeletionTimestamp != nil || nc.Spec.Type != "catalyst-center" || nc.Spec.Endpoint != "https://host.docker.internal:19443" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || !reflect.DeepEqual(nc.Status.Configuration, saved.Status.Configuration) {
		t.Fatal("settled saved qualification custody changed")
	}
	var config configapi.NetworkControllerConfig
	ck := ctrlclient.ObjectKey{Namespace: key.Namespace, Name: "installed-private-sites"}
	if err := api.Get(ctx, ck, &config); err != nil {
		t.Fatal(err)
	}
	if !siteComparisonConfig(&config) || !supportedConfigurationApply(&config) || config.Spec.PrunePolicy != "Retain" || config.Spec.DeletionPolicy != "Retain" || config.Spec.Source.ConfigMapRef == nil || !strings.HasPrefix(config.Spec.Source.ConfigMapRef.Name, "installed-floor-assets-") {
		t.Fatal("retained private source required")
	}
	source, err := loadControllerSource(ctx, api, &config)
	if err != nil {
		t.Fatal(err)
	}
	desired, aps, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil || len(desired) != 3 || len(aps) != 0 || len(images) != 0 || parentSitePath(desired[0].path) != "Global" || !strings.HasPrefix(lastSiteName(desired[0].path), "CVK_INST_") {
		t.Fatal("requires exact omitted-image generated hierarchy")
	}
	creators, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil || len(creators) != 3 {
		t.Fatal("three private creator receipts required", err)
	}
	for i, kind := range []string{"area", "building", "floor"} {
		if desired[i].kind != kind || i > 0 && parentSitePath(desired[i].path) != desired[i-1].path {
			t.Fatal("hierarchy shape changed")
		}
	}
	for _, row := range creators {
		if row.receipt.Operation.ConfigUID != string(config.UID) || row.receipt.Operation.ConfigName != config.Name {
			t.Fatal("foreign creator custody")
		}
	}
	c := liveClient(t)
	baseline, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := []Site{}
	for _, s := range baseline {
		if s.NameHierarchy != desired[0].path && !strings.HasPrefix(s.NameHierarchy, desired[0].path+"/") {
			unrelated = append(unrelated, s)
		}
	}
	checkEmpty := func() {
		t.Helper()
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id := ""
		for _, s := range sites {
			if s.NameHierarchy == desired[2].path && s.Type == "floor" {
				id = s.ID
			}
		}
		if id == "" {
			t.Fatal("generated floor missing")
		}
		aps, err := c.plannedAPNativePositions(ctx, id)
		if err != nil || len(aps) != 0 {
			t.Fatal("floor contains planned APs", err)
		}
		devices, err := c.assignedDevices(ctx, id)
		if err != nil || len(devices) != 0 {
			t.Fatal("floor contains assigned devices", err)
		}
	}
	checkEmpty()
	uid := config.UID
	if err := api.Delete(ctx, &config, ctrlclient.Preconditions{UID: &uid}); err != nil {
		t.Fatal(err)
	}
	for {
		var cur configapi.NetworkControllerConfig
		err := api.Get(ctx, ck, &cur)
		if apierrors.IsNotFound(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("retained intent did not retire")
		case <-time.After(time.Second):
		}
	}
	if err := api.Get(ctx, key, &nc); err != nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || !reflect.DeepEqual(nc.Status.Configuration.ActionApplications, saved.Status.Configuration.ActionApplications) {
		t.Fatal("retirement changed settled receipts")
	}
	cleanupInstalledPrivateSites(t, ctx, c, desired, creators, checkEmpty)
	actual, err := c.ListSites(ctx)
	if err != nil || !reflect.DeepEqual(unrelated, actual) {
		t.Fatal("unrelated current baseline changed", err)
	}
	t.Log("settled retained generated fixture cleaned; unrelated current baseline preserved; original failed qualification is not converted to a pass")
}
