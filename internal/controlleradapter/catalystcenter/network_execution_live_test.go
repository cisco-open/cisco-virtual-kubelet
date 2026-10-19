// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func qualifyLiveNetworkSettings(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	raw := liveNetworkSettingsModel(t, root)
	executor, err := c.compileConfigurationExecutor(raw, "network_settings")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") == "isolated-public-preflight" {
		qualifyLivePublicNetworkSettings(t, c, ctx, root, raw, settled)
		return
	}
	updates := 0
	for step := 0; step < 7; step++ {
		write, err := executor.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if write == nil {
			break
		}
		if write.identity.target == "" || !strings.HasPrefix(write.resource, root+"#") {
			t.Fatal("settings operation escaped isolated site")
		}
		*settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("submitted network setting=%s task=%s at=%s", write.resource[len(root)+1:], task.ID, submitted.UTC().Format(time.RFC3339Nano))
		if err = waitTask(ctx, task, submitted); err != nil {
			t.Fatal(err)
		}
		verified, err := executor.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent})
		if err != nil || !verified {
			t.Fatalf("network setting read-back verified=%v err=%v", verified, err)
		}
		*settled = true
		updates++
	}
	if updates != 6 {
		t.Fatalf("qualified %d settings, expected all six", updates)
	}
	if next, err := executor.next(ctx); err != nil || next != nil {
		t.Fatalf("network settings did not converge: %v", err)
	}
	t.Log("six network setting updates verified: NTP, DHCP, DNS, timezone, banner and builtin telemetry; empty isolated site only")
}

// Actual appliance writes through the public reconciler, with one simulated
// read-only preflight fault per resource. Kubernetes storage/actors are fixtures.
func TestLiveCatalystCenterNetworkSubmission(t *testing.T) {
	if os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") != "isolated-public-preflight" {
		t.Skip("isolated public network qualification disabled")
	}
	qualifyLiveSiteExecution(t, true)
}

// Same real public lifecycle, also retaining each original acknowledged task
// as Unknown before restarting. This never fabricates an appliance task result.
func TestLiveCatalystCenterNetworkTaskRecovery(t *testing.T) {
	if os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") != "isolated-public-preflight" || os.Getenv("CATC_NETWORK_TASK_RECOVERY") != "isolated-original-tasks" {
		t.Skip("isolated original network task qualification disabled")
	}
	qualifyLiveSiteExecution(t, true)
}

func TestLiveCatalystCenterNetworkApplications(t *testing.T) {
	if os.Getenv("CATC_NETWORK_APPLICATION_QUALIFICATION") != "isolated-latest-applications" || os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") != "isolated-public-preflight" || os.Getenv("CATC_NETWORK_TASK_RECOVERY") != "isolated-original-tasks" {
		t.Skip("isolated application lifecycle qualification disabled")
	}
	qualifyLiveSiteExecution(t, true)
}

func qualifyLivePublicNetworkSettings(t *testing.T, c *client, ctx context.Context, root string, raw []byte, settled *bool) {
	t.Helper()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, site := range sites {
		if site.NameHierarchy == root && site.Type == "area" {
			target = site.ID
		}
	}
	if !validAPIID(target) {
		t.Fatal("isolated area identity missing")
	}
	reconciler, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := reconciler.a
	a.client = c
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	config.Spec.Source.ConfigMapRef = nil
	if os.Getenv("CATC_NETWORK_DEFAULT_EXPORT") == "isolated-pinned" {
		config.Spec.ModelSource.Exporter = liveNetworkDefaultExporterIdentity()
	}
	if os.Getenv("CATC_NETWORK_SITE_EXPORT") == "isolated-exact" {
		config.Spec.ModelSource.Exporter = NetworkSiteSelectionExporter
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	trace := &metadataNoDispatchTrace{a: a, section: "network_settings", preflightPath: sitesPath, injected: map[string]bool{}, retired: map[string]bool{}, allowMutation: func(req *http.Request) bool {
		return req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, sitesPath+"/"+target+"/") && strings.HasSuffix(req.URL.Path, "Settings")
	}}
	restore, err := installMetadataSessionTrace(ctx, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	*settled = false
	seen := map[string]string{}
	recovered := map[string]bool{}
	revision := 0
	var initialApplications map[string]ciskov1.NetworkControllerConfigurationActionApplication
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	for step := 0; step < 200; step++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if state != nil && state.Operation != nil && state.Operation.TaskID != "" {
			op := state.Operation
			logical := ""
			for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
				if networkSettingResourceMatches(op.Resource, networkSettingIdentity(root, kind)) {
					logical = networkSettingIdentity(root, kind)
				}
			}
			if logical == "" || op.TargetID != target || op.Section != "network_settings" || op.Action != "Update" {
				t.Fatal("retained operation escaped isolated settings scope")
			}
			if old, exists := seen[op.Resource]; exists && old != op.TaskID {
				t.Fatal("completed settings task was replayed")
			}
			if seen[op.Resource] == "" {
				t.Log("public isolated setting accepted", strings.TrimPrefix(logical, root+"#"), op.TaskID)
			}
			seen[op.Resource] = op.TaskID
			if os.Getenv("CATC_NETWORK_TASK_RECOVERY") == "isolated-original-tasks" && op.Phase == "Accepted" && !recovered[op.TaskID] {
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered[op.TaskID] = true
				t.Log("retained original network task as Unknown for restarted observation", op.TaskID)
			}
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("public settings failed; retain isolated hierarchy")
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			if state.Operation != nil || len(seen) != 6 || trace.mutations != (revision+1)*6 || len(trace.injected) != (revision+1)*6 || len(trace.retired) != (revision+1)*6 || trace.pending != "" {
				t.Fatalf("incomplete public lifecycle: tasks=%d writes=%d faults=%d retired=%d", len(seen), trace.mutations, len(trace.injected), len(trace.retired))
			}
			records, err := networkSettingApplicationRecords(state)
			if err != nil || len(records) != 6 {
				t.Fatal("six durable applications missing", err)
			}
			for _, row := range records {
				if row.Operation.TargetID != target || row.Operation.ConfigUID != string(config.UID) || row.Operation.SourceVersion != "" || row.Operation.ConfigGeneration != config.Generation {
					t.Fatal("application escaped original target/source")
				}
			}
			for range 2 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
				if journalState(t, a).Operation != nil || trace.mutations != (revision+1)*6 {
					t.Fatal("converged settings replayed a native mutation")
				}
			}
			if os.Getenv("CATC_NETWORK_TASK_RECOVERY") == "isolated-original-tasks" {
				if len(recovered) != 6 || len(trace.recoveryTrees) != (revision+1)*6 {
					t.Fatal("original full task trees were not recovered")
				}
				for _, task := range seen {
					if !recovered[task] || trace.recoveryTrees[task] < 1 {
						t.Fatal("missing original task tree observation")
					}
				}
				t.Log("all six original Unknown tasks recovered through full task trees and unchanged native/source/owner/journal evidence; no resubmission")
			}
			if os.Getenv("CATC_NETWORK_APPLICATION_QUALIFICATION") == "isolated-latest-applications" && revision == 0 {
				initialApplications = records
				if os.Getenv("CATC_NETWORK_SELECTION_QUALIFICATION") == "isolated-null-reselect" {
					original := qualifyLiveInactiveNetworkSelection(t, ctx, a, config, c, root)
					config.Spec.Source.Inline = &runtime.RawExtension{Raw: original}
				} else if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
					t.Fatal(err)
				}
				var model map[string]any
				if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &model); err != nil {
					t.Fatal(err)
				}
				groups := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)
				profile := groups["network"].([]any)[0].(map[string]any)
				profile["ntp_servers"] = []string{"192.0.2.11"}
				profile["dhcp_servers"] = []string{"192.0.2.12"}
				profile["dns_servers"] = []string{"192.0.2.13", "192.0.2.14"}
				profile["domain_name"] = "changed.example.test"
				profile["timezone"] = "Etc/UTC"
				profile["banner"] = "CVK isolated updated banner"
				groups["telemetry"].([]any)[0].(map[string]any)["wired_data_collection"] = true
				updated, err := json.Marshal(model)
				if err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = updated
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				revision++
				seen = map[string]string{}
				recovered = map[string]bool{}
				t.Log("first six applications retained; applying a second source revision to replace the latest records")
				continue
			}
			if revision == 1 {
				if len(records) != len(initialApplications) || len(state.ActionApplications) != 6 {
					t.Fatal("latest application history grew")
				}
				for key, row := range records {
					old, exists := initialApplications[key]
					if !exists || row.Operation.ID == old.Operation.ID || row.Operation.TaskID == old.Operation.TaskID || row.Operation.TargetID != old.Operation.TargetID || row.Operation.ConfigGeneration <= old.Operation.ConfigGeneration {
						t.Fatal("application replacement lost original target or new task/source binding")
					}
				}
				t.Log("twelve original tasks recovered; two source revisions retain exactly six latest application records with stable targets, atomic receipts, no raw native values and no replay")
			}
			*settled = true
			t.Log("six public setting updates reached InSync after six original no-dispatch refusals and automatic retire/replan; six native tasks, no replay; simulated storage/actors only")
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("settings timed out; retain isolated hierarchy", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("public settings did not converge; retain isolated hierarchy")
}

func liveNetworkSettingsModel(t *testing.T, root string) []byte {
	t.Helper()
	var model map[string]any
	if err := json.Unmarshal(allNetworkExecutionModel(), &model); err != nil {
		t.Fatal(err)
	}
	stripe := model["catalyst_center"].(map[string]any)
	stripe["sites"] = map[string]any{"areas": []any{map[string]any{"name": lastSiteName(root), "parent_name": "Global", "network_settings": map[string]any{"network": "base", "telemetry": "base"}}}}
	// No external collector infrastructure is required to qualify the builtin
	// profile branch on a new, empty site. External collector payloads retain
	// fixture coverage and are not claimed live-qualified by this test.
	telemetry := stripe["network_settings"].(map[string]any)["telemetry"].([]any)[0].(map[string]any)
	telemetry["catalyst_center_as_network_collector"] = true
	telemetry["enable_netflow_collector_on_devices"] = false
	telemetry["wireless_telemetry"] = false
	delete(telemetry, "netflow_collector_ip_address")
	delete(telemetry, "netflow_collector_port")
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CATC_NETWORK_DEFAULT_EXPORT") == "isolated-pinned" {
		return liveExportNetworkDefaultModel(t, raw)
	}
	if os.Getenv("CATC_NETWORK_SITE_EXPORT") == "isolated-exact" {
		return liveExportSelectedNetworkModel(t, raw, root)
	}
	return raw
}
