// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLiveCatalystCenterNetworkEmptyExecution(t *testing.T) {
	if os.Getenv("CATC_NETWORK_EMPTY_EXECUTION") != "isolated-public-resets" {
		t.Skip("isolated public reset qualification disabled")
	}
	qualifyLiveSiteExecution(t, false)
}

// A distinct native gate validates that an explicit empty child override does
// not silently re-inherit a nonempty parent after an acknowledged reset.
func TestLiveCatalystCenterNetworkEmptyInheritance(t *testing.T) {
	if os.Getenv("CATC_NETWORK_EMPTY_EXECUTION") != "isolated-public-resets" || os.Getenv("CATC_NETWORK_EMPTY_INHERITANCE") != "isolated-parent" {
		t.Skip("isolated inherited reset qualification disabled")
	}
	qualifyLiveSiteExecution(t, false)
}

func liveNetworkEmptyModel(t *testing.T, root string, empty bool) []byte {
	t.Helper()
	var model map[string]any
	if err := json.Unmarshal(networkEmptyModel(t, "ntp", root, empty), &model); err != nil {
		t.Fatal(err)
	}
	profile := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["network"].([]any)[0].(map[string]any)
	profile["dhcp_servers"] = profile["ntp_servers"]
	if os.Getenv("CATC_NETWORK_EMPTY_INHERITANCE") == "isolated-parent" {
		model["catalyst_center"].(map[string]any)["sites"] = map[string]any{"buildings": []any{map[string]any{"name": lastSiteName(root), "parent_name": parentSitePath(root), "network_settings": map[string]any{"network": "base"}}}}
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func qualifyLiveNetworkEmptyExecution(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetPath, targetKind := root, "area"
	inherited := os.Getenv("CATC_NETWORK_EMPTY_INHERITANCE") == "isolated-parent"
	if inherited {
		targetPath, targetKind = root+"/TestBuilding", "building"
	}
	target, seedTarget := "", ""
	for _, site := range sites {
		if site.NameHierarchy == root && site.Type == "area" {
			seedTarget = site.ID
		}
		if site.NameHierarchy == targetPath && site.Type == targetKind {
			target = site.ID
		}
	}
	if !inherited {
		seedTarget = target
	}
	if !validAPIID(target) || !validAPIID(seedTarget) {
		t.Fatal("generated empty area missing")
	}
	for _, kind := range []string{"ntp", "dhcp"} {
		*settled = false
		body, err := networkSettingPayload(networkSetting{kind: kind, servers: []string{"192.0.2.21", "192.0.2.22"}})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		raw, err := c.mutate(ctx, http.MethodPut, sitesPath+"/"+seedTarget+"/"+kind+"Settings", body)
		if err != nil {
			t.Fatal(err)
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			t.Fatal("seed acknowledgement unavailable")
		}
		if err := waitTask(ctx, ack.Response, started); err != nil {
			t.Fatal(err)
		}
		got, err := c.readNetworkSetting(ctx, seedTarget, kind)
		if err != nil || got.unset || got.inherited || !reflect.DeepEqual(got.servers, []string{"192.0.2.21", "192.0.2.22"}) {
			t.Fatal("seed readback unproven", err)
		}
		*settled = true
	}
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	trace := &metadataNoDispatchTrace{a: a, section: "network_settings", preflightPath: sitesPath, injected: map[string]bool{}, retired: map[string]bool{}, allowMutation: func(req *http.Request) bool {
		return req.Method == http.MethodPut && (req.URL.Path == sitesPath+"/"+target+"/ntpSettings" || req.URL.Path == sitesPath+"/"+target+"/dhcpSettings")
	}}
	restore, err := installMetadataSessionTrace(ctx, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	var previous map[string]cvk.NetworkControllerConfigurationActionApplication
	for phase, empty := range []bool{true, false, true} {
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
		config.Spec.Mode = configapi.NetworkControllerApplyModeApply
		config.Spec.ManagedSections = []string{"network_settings"}
		config.Spec.Source.ConfigMapRef = nil
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: liveNetworkEmptyModel(t, targetPath, empty)}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		*settled = false
		recovered := map[string]bool{}
		complete := false
		for range 100 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered[state.Operation.TaskID] {
				op := state.Operation
				if op.TargetID != target || !(networkSettingResourceMatches(op.Resource, networkSettingIdentity(targetPath, "ntp")) || networkSettingResourceMatches(op.Resource, networkSettingIdentity(targetPath, "dhcp"))) {
					t.Fatal("reset escaped generated target scope")
				}
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered[op.TaskID] = true
			}
			if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("reset public flow failed; retain fixture")
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				records, err := networkSettingApplicationRecords(state)
				if err != nil || len(records) != 2 || state.Operation != nil || len(recovered) != 2 || trace.mutations != (phase+1)*2 {
					t.Fatal("original reset tasks/latest applications incomplete", err)
				}
				for key, row := range records {
					if row.Operation.TargetID != target || row.Operation.ConfigGeneration != config.Generation || !recovered[row.Operation.TaskID] || trace.recoveryTrees[row.Operation.TaskID] < 1 {
						t.Fatal("reset original task/target/source tree evidence incomplete")
					}
					if phase > 0 {
						old, ok := previous[key]
						if !ok || old.Operation.ID == row.Operation.ID || old.Operation.TaskID == row.Operation.TaskID || old.Operation.TargetID != row.Operation.TargetID {
							t.Fatal("reset replacement lost latest task lineage")
						}
					}
				}
				for _, kind := range []string{"ntp", "dhcp"} {
					got, err := c.readNetworkSetting(ctx, target, kind)
					if err != nil || got.inherited || got.unset != empty || (empty && len(got.servers) != 0) || (!empty && !reflect.DeepEqual(got.servers, []string{"192.0.2.41", "192.0.2.42"})) {
						t.Fatal("native reset/refill readback mismatch", err)
					}
				}
				if inherited {
					for _, kind := range []string{"ntp", "dhcp"} {
						parent, err := c.readNetworkSetting(ctx, seedTarget, kind)
						if err != nil || parent.unset || parent.inherited || !reflect.DeepEqual(parent.servers, []string{"192.0.2.21", "192.0.2.22"}) {
							t.Fatal("child reset changed its unrelated parent", err)
						}
					}
				}
				for range 2 {
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				if journalState(t, a).Operation != nil || trace.mutations != (phase+1)*2 {
					t.Fatal("reset/no-op replayed a mutation")
				}
				previous = records
				*settled = true
				complete = true
				t.Logf("public NTP/DHCP phase=%d empty=%v inherited-parent=%v: two original Unknown tasks recovered, two latest receipts, native readback and repeated no-op verified", phase, empty, inherited)
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !complete {
			t.Fatal("public reset cycle timed out; retain fixture")
		}
	}
	if len(trace.injected) != 6 || len(trace.retired) != 6 || len(trace.recoveryTrees) != 6 || trace.pending != "" {
		t.Fatal("reset preflight/original task evidence incomplete")
	}
	t.Log("six public reset/refill/reset mutations and original task trees, six no-dispatch retirements, exactly two latest applications; actual native API, simulated Kubernetes storage/actors")
}
