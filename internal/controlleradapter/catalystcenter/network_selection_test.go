// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func inactiveNetworkModel(t *testing.T, mode, path string) []byte {
	t.Helper()
	profile := map[string]any{"name": "base"}
	if mode != "omitted" {
		for _, key := range []string{"ntp_servers", "dhcp_servers", "domain_name", "dns_servers", "timezone", "banner"} {
			profile[key] = nil
		}
	}
	if mode == "dns-unselected" {
		profile["dns_servers"] = []string{"192.0.2.99"}
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{
		"sites":            map[string]any{"areas": []any{map[string]any{"name": lastSiteName(path), "parent_name": parentSitePath(path), "network_settings": map[string]any{"network": "base"}}}},
		"network_settings": map[string]any{"network": []any{profile}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNetworkSelectionNoNativeDependencies(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "dns-unselected"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("inactive selection performed native API request")
				w.WriteHeader(503)
			})
			raw := inactiveNetworkModel(t, mode, "Global/Absent")
			reports, err := c.compileConfigReports(raw, []string{"network_settings"})
			if err != nil || len(reports) != 1 || reports[0].count != 0 {
				t.Fatal("inactive report has resources", err)
			}
			if drift, err := reports[0].observe(context.Background()); err != nil || len(drift) != 0 {
				t.Fatal("inactive report depends on native state", err)
			}
			apply, err := c.compileConfigurationApply(raw, []string{"network_settings"}, nil)
			if err != nil || apply.executor.count() != 0 {
				t.Fatal("inactive Apply has resources", err)
			}
			if write, err := apply.executor.next(context.Background()); err != nil || write != nil {
				t.Fatal("inactive Apply planned a native action", err)
			}
		})
	}
}

func TestNetworkSelectionCountsAndValidation(t *testing.T) {
	c := &client{}
	reports, err := c.compileConfigReports(allNetworkExecutionModel(), []string{"network_settings"})
	if err != nil || len(reports) != 1 || reports[0].count != 6 {
		t.Fatal("report must count selected resources, not profile bindings", err)
	}
	for _, key := range []string{"ntp_servers", "dhcp_servers", "domain_name", "timezone", "banner", "unknown_field"} {
		t.Run(key, func(t *testing.T) {
			profile := map[string]any{"name": "base", key: map[string]any{"bad": "type"}}
			if _, err := parseNetworkProfile(profile); err == nil {
				t.Fatal("malformed selected field was silently skipped")
			}
		})
	}
}

func exerciseNetworkSelection(t *testing.T, kind, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	scoped := strings.HasPrefix(mode, "scoped-")
	mode = strings.TrimPrefix(mode, "scoped-")
	ctx := context.Background()
	a, config := journalFixture(t)
	e, f := networkSubmissionFixtureExecutor(t, kind)
	a.client = e.client
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: networkSingleSettingModel(t, kind)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
	}
	for range 15 {
		tick()
		state := journalState(t, a)
		if mode == "verified-null" {
			if state != nil && state.Operation != nil && state.Operation.Phase == "Verified" {
				break
			}
		} else if mode == "accepted-null" || mode == "unknown-null" {
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" {
				break
			}
		} else if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	before := journalState(t, a)
	if f.writes != 1 {
		t.Fatal("initial setting did not execute exactly once")
	}
	uncertain := mode == "accepted-null" || mode == "unknown-null" || mode == "verified-null"
	if uncertain {
		expectedPhase := "Accepted"
		if mode == "verified-null" {
			expectedPhase = "Verified"
		}
		if before.Operation == nil || before.Operation.Phase != expectedPhase {
			t.Fatal("original acknowledged operation missing")
		}
		if mode == "unknown-null" {
			if err := a.advanceConfiguration(ctx, before.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			before = journalState(t, a)
		}
	} else if before.Operation != nil || len(before.ActionApplications) != 1 {
		t.Fatal("initial receipt not settled")
	}
	selection := "null"
	if mode == "omitted" || mode == "dns-unselected" {
		selection = mode
	}
	config.Spec.Source.Inline.Raw = inactiveNetworkModel(t, selection, "Global/Lab")
	if scoped {
		exported, meta, err := ExportSelectedNetworkSettings(networkSingleSettingModel(t, kind), nil, nacmodel.SiteSelectionOptions{ManageGlobalSettings: true})
		if err != nil {
			t.Fatal("zero site selection export failed", err)
		}
		config.Spec.Source.Inline.Raw = exported
		config.Spec.ModelSource.Exporter = meta.Exporter
	}
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if mode == "corrupt-history" {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.ActionApplications[0].Resource = "network-setting-applied:invalid"
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		before = journalState(t, a)
	}
	if mode == "verified-null" {
		nc, _ := a.currentController(ctx)
		key, _, ok := networkSettingUpdateParts(nc.Status.Configuration.Operation.Resource)
		if !ok || len(nc.Status.Configuration.ActionApplications) != 1 {
			t.Fatal("verified receipt missing")
		}
		nc.Status.Configuration.ActionApplications[0].Resource = networkSettingAppliedPrefix + key + ":" + strings.Repeat("f", 64)
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		before = journalState(t, a)
	}
	if mode == "missing-site" {
		f.sites = f.sites[:1]
	}
	reads := f.siteReads
	nativeBefore, _ := json.Marshal(f.data)
	for range 5 {
		tick()
	}
	after := journalState(t, a)
	nativeAfter, _ := json.Marshal(f.data)
	if f.writes != 1 || f.siteReads != reads || string(nativeBefore) != string(nativeAfter) {
		t.Fatal("deselection read or changed native settings")
	}
	if !reflect.DeepEqual(before.ActionApplications, after.ActionApplications) {
		t.Fatal("deselection changed retained applications")
	}
	if uncertain {
		if !reflect.DeepEqual(before.Operation, after.Operation) || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("deselection cleared or settled acknowledged work")
		}
		return
	}
	if mode == "corrupt-history" {
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("zero selection bypassed invalid receipt custody")
		}
		return
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || after.Operation != nil || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 0 {
		t.Fatal("inactive selection did not converge with zero resources")
	}
	if mode == "reselect" {
		config.Spec.Source.Inline.Raw = networkApplicationChangedModel(t, kind)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		recovered := false
		for range 20 {
			tick()
			state := journalState(t, a)
			if state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered {
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered = true
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				break
			}
		}
		final := journalState(t, a)
		if !recovered || f.writes != 2 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || final.Operation != nil || len(final.ActionApplications) != 1 {
			t.Fatal("reselection failed original task/latest receipt lifecycle")
		}
		old, new := before.ActionApplications[0].Operation, final.ActionApplications[0].Operation
		if old.TargetID != new.TargetID || old.TaskID == new.TaskID || old.ID == new.ID || new.ConfigGeneration <= old.ConfigGeneration {
			t.Fatal("reselection lost target/source/task lineage")
		}
	}
}

func TestNetworkSelectionRetainedLifecycle(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner"} {
		for _, mode := range []string{"omitted", "null", "dns-unselected", "missing-site", "reselect", "accepted-null", "unknown-null", "verified-null", "corrupt-history"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseNetworkSelection(t, kind, mode, nil) })
		}
	}
}
