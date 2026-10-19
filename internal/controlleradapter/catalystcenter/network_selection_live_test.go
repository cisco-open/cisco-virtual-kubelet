// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"os"
	"reflect"
	"sync/atomic"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Same actual six-setting public application cycle, with a source revision
// selecting zero resources between the two mutating revisions. Storage and
// actor interruptions remain fixtures; task trees and native settings are real.
func TestLiveCatalystCenterNetworkSelection(t *testing.T) {
	if os.Getenv("CATC_NETWORK_SELECTION_QUALIFICATION") != "isolated-null-reselect" || os.Getenv("CATC_NETWORK_APPLICATION_QUALIFICATION") != "isolated-latest-applications" || os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") != "isolated-public-preflight" || os.Getenv("CATC_NETWORK_TASK_RECOVERY") != "isolated-original-tasks" {
		t.Skip("isolated resource selection qualification disabled")
	}
	qualifyLiveSiteExecution(t, true)
}

type networkSelectionRequestTrace struct {
	base     http.RoundTripper
	requests atomic.Int64
}

func (t *networkSelectionRequestTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return t.base.RoundTrip(req)
}

func qualifyLiveInactiveNetworkSelection(t *testing.T, ctx context.Context, a *adapter, config *configapi.NetworkControllerConfig, c *client, root string) []byte {
	t.Helper()
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
		t.Fatal(err)
	}
	original := append([]byte{}, config.Spec.Source.Inline.Raw...)
	before := journalState(t, a).DeepCopy()
	if before.Operation != nil || len(before.ActionApplications) != 6 {
		t.Fatal("six settled applications missing before deselection")
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, s := range sites {
		if s.NameHierarchy == root {
			target = s.ID
		}
	}
	if !validAPIID(target) {
		t.Fatal("isolated target missing before deselection")
	}
	kinds := []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"}
	native := map[string]networkObservation{}
	for _, kind := range kinds {
		got, err := c.readNetworkSetting(ctx, target, kind)
		if err != nil {
			t.Fatal(err)
		}
		native[kind] = got
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &networkSelectionRequestTrace{base: base}
	rest.HTTPClient.Transport = trace
	defer func() { rest.HTTPClient.Transport = base }()
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: inactiveNetworkModel(t, "null", root)}
	if os.Getenv("CATC_NETWORK_SITE_EXPORT") == "isolated-exact" {
		exported, meta, err := ExportSelectedNetworkSettings(original, nil, nacmodel.SiteSelectionOptions{ManageGlobalSettings: true})
		if err != nil || meta.SiteSelection == nil || meta.SiteSelection.SelectedSiteCount != 0 {
			t.Fatal("zero site export failed", err)
		}
		config.Spec.Source.Inline.Raw = exported
		config.Spec.ModelSource.Exporter = meta.Exporter
	}
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
		t.Fatal(err)
	}
	after := journalState(t, a)
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 0 || after.Operation != nil || !reflect.DeepEqual(before.ActionApplications, after.ActionApplications) || trace.requests.Load() != 0 {
		t.Fatal("inactive model queried/changed native state, history or resource accounting")
	}
	// Disable the request counter only after proving zero native dependency. These
	// independent actual GETs verify retained device-free native setting values.
	rest.HTTPClient.Transport = base
	for _, kind := range kinds {
		got, err := c.readNetworkSetting(ctx, target, kind)
		if err != nil || !reflect.DeepEqual(native[kind], got) {
			t.Fatal("deselection changed native settings", err)
		}
	}
	if os.Getenv("CATC_NETWORK_SITE_EXPORT") == "isolated-exact" {
		t.Log("source-site exclusion selected zero resources: eight public reconciliations, zero native requests, six original applications and six native setting values retained")
	} else {
		t.Log("all-null source revision selected zero resources: eight public reconciliations, zero native requests, six original applications and six native setting values retained")
	}
	return original
}
