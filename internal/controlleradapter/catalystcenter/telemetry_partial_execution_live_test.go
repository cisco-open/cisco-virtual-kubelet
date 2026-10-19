//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestLiveCatalystCenterTelemetryPartialExecution(t *testing.T) {
	if os.Getenv("CATC_TELEMETRY_PARTIAL_EXECUTION") != "isolated-preserving" {
		t.Skip("isolated preserving telemetry execution disabled")
	}
	qualifyLiveSiteExecution(t, false)
}

func qualifyLiveTelemetryPartialExecution(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
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
		t.Fatal("generated area unavailable")
	}
	bindings, err := parseNetworkSettings(liveNetworkSettingsModel(t, root))
	if err != nil {
		t.Fatal(err)
	}
	var seed []byte
	for _, binding := range bindings {
		for _, s := range binding.settings {
			if s.kind == "telemetry" {
				seed, err = networkSettingPayload(s)
			}
		}
	}
	if err != nil || len(seed) == 0 {
		t.Fatal("complete seed unavailable", err)
	}
	*settled = false
	started := time.Now()
	raw, err := c.mutate(ctx, http.MethodPut, sitesPath+"/"+target+"/telemetrySettings", seed)
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
		t.Fatal("original setup task missing")
	}
	if err := waitTask(ctx, ack.Response, started); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	baseline, err := c.effectiveNetworkSetting(ctx, byPath, root, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	*settled = true
	expected := cloneNetworkTelemetry(baseline.telemetry)
	for _, group := range telemetryGroups {
		if len(expected[group]) == 0 {
			t.Fatal("complete effective seed group unavailable")
		}
	}
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	if os.Getenv("CATC_NETWORK_NATIVE_COMPARISON") == "isolated-keyed" {
		configureNetworkNativeComparisonFixture(t, a, config)
	}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	trace := &metadataNoDispatchTrace{a: a, section: "network_settings", preflightPath: sitesPath, injected: map[string]bool{}, retired: map[string]bool{}, allowMutation: func(req *http.Request) bool {
		return req.Method == http.MethodPut && req.URL.Path == sitesPath+"/"+target+"/telemetrySettings"
	}}
	restore, err := installMetadataSessionTrace(ctx, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	previousTask := ""
	phases := []map[string]any{{"wired_data_collection": true, "wireless_telemetry": nil}, {"catalyst_center_as_syslog_server": true}, {"wired_data_collection": false}}
	for phase, fields := range phases {
		if phase == 1 {
			expected["syslogs"]["useBuiltinSyslogServer"] = true
		} else {
			expected["wiredDataCollection"]["enableWiredDataCollection"] = phase == 0
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
		config.Spec.Mode = configapi.NetworkControllerApplyModeApply
		config.Spec.ManagedSections = []string{"network_settings"}
		config.Spec.Source.ConfigMapRef = nil
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: telemetryPartialModel(t, fields, root)}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		*settled = false
		recovered := false
		complete := false
		for range 80 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered {
				if state.Operation.TargetID != target || !networkSettingResourceMatches(state.Operation.Resource, networkSettingIdentity(root, "telemetry")) {
					t.Fatal("operation escaped generated telemetry target")
				}
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered = true
			}
			if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("public telemetry flow failed; retain fixture")
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				records, err := networkSettingApplicationRecords(state)
				if err != nil || len(records) != 1 || state.Operation != nil || !recovered || trace.mutations != phase+1 {
					t.Fatal("partial telemetry task/receipt lifecycle incomplete", err)
				}
				for _, row := range records {
					if os.Getenv("CATC_NETWORK_NATIVE_COMPARISON") == "isolated-keyed" {
						if _, private := networkPrivateParts(row.Operation.Resource); !private || !strings.HasPrefix(row.Resource, networkPrivateAppliedPrefix) {
							t.Fatal("native cycle did not retain private expected-state proof")
						}
					}
					if row.Operation.TargetID != target || row.Operation.ConfigGeneration != config.Generation || row.Operation.TaskID == previousTask || trace.recoveryTrees[row.Operation.TaskID] < 1 {
						t.Fatal("original task/source/target tree evidence missing")
					}
					previousTask = row.Operation.TaskID
				}
				got, err := c.effectiveNetworkSetting(ctx, byPath, root, "telemetry")
				if err != nil || !reflect.DeepEqual(expected, got.telemetry) {
					t.Fatal("partial update changed an unselected native field/group", err)
				}
				for range 2 {
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				if trace.mutations != phase+1 || journalState(t, a).Operation != nil {
					t.Fatal("partial telemetry no-op replayed")
				}
				*settled = true
				complete = true
				t.Logf("partial telemetry phase=%d original task=%s: all native groups preserved except selected mask, Unknown recovery, latest receipt and repeated no-op verified", phase, previousTask)
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !complete {
			t.Fatal("partial telemetry timed out; retain fixture")
		}
	}
	if len(trace.injected) != 3 || len(trace.retired) != 3 || len(trace.recoveryTrees) != 3 || trace.pending != "" {
		t.Fatal("partial telemetry preflight/task evidence incomplete")
	}
	t.Log("three partial-source public updates, complete native profile preservation, three original task recoveries and one latest application; actual native API with simulated Kubernetes storage/actors")
	if os.Getenv("CATC_NETWORK_NATIVE_COMPARISON") == "isolated-keyed" {
		t.Log("v2 expected full native profile/ancestry MACs retained through restarted observation and replaced atomically; approved key projection is a fixture, no raw native values persisted")
	}

}
