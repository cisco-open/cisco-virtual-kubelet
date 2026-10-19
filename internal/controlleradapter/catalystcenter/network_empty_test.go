// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func networkEmptyModel(t *testing.T, kind, path string, empty bool) []byte {
	t.Helper()
	var model map[string]any
	if err := json.Unmarshal(networkSingleSettingModel(t, kind), &model); err != nil {
		t.Fatal(err)
	}
	root := model["catalyst_center"].(map[string]any)
	profile := root["network_settings"].(map[string]any)["network"].([]any)[0].(map[string]any)
	servers := []string{"192.0.2.41", "192.0.2.42"}
	if empty {
		servers = []string{}
	}
	profile[kind+"_servers"] = servers
	root["sites"] = map[string]any{"areas": []any{map[string]any{"name": lastSiteName(path), "parent_name": parentSitePath(path), "network_settings": map[string]any{"network": "base"}}}}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func exerciseNetworkEmpty(t *testing.T, kind, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	e, f := networkSubmissionFixtureExecutor(t, kind)
	a.client = e.client
	f.data["area/"+kind] = map[string]any{kind: map[string]any{"servers": []string{"192.0.2.21", "192.0.2.22"}}}
	if mode == "parent-nonempty" || mode == "inherited" {
		f.data["root/"+kind] = map[string]any{kind: map[string]any{"servers": []string{"192.0.2.99"}}}
	}
	if mode == "inherited" {
		delete(f.data, "area/"+kind)
	}
	if mode == "already-clear" {
		f.data["area/"+kind] = map[string]any{kind: map[string]any{}}
	}
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: networkEmptyModel(t, kind, "Global/Lab", true)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	if mode == "lost-ack" {
		f.lostAck = true
	}
	injected := false
	if mode == "preflight-race" {
		base := a.statusWriter
		a.statusWriter = networkSubmittingClient{Client: base, after: func() {
			if injected {
				return
			}
			injected = true
			f.mu.Lock()
			defer f.mu.Unlock()
			f.data["area/"+kind] = map[string]any{kind: map[string]any{"servers": []string{"192.0.2.98"}}}
		}}
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
	previousTask := ""
	for phase, empty := range []bool{true, false, true} {
		if phase > 0 {
			config.Spec.Source.Inline.Raw = networkEmptyModel(t, kind, "Global/Lab", empty)
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		recovered := false
		complete := false
		for step := 0; step < 35; step++ {
			tick()
			state := journalState(t, a)
			if phase == 0 && mode == "preflight-race" && step == 0 {
				if !injected || f.writes != 0 || state.Operation != nil {
					t.Fatal("stale clear dispatched or fence was not retired")
				}
			}
			if state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered {
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered = true
				if mode == "native-race" {
					f.data["area/"+kind] = map[string]any{kind: map[string]any{"servers": []string{"192.0.2.97"}}}
				}
				if mode == "malformed-read" {
					f.data["area/"+kind] = map[string]any{kind: map[string]any{"servers": nil}}
				}
			}
			if mode == "lost-ack" || mode == "native-race" || mode == "malformed-read" {
				if step < 8 {
					continue
				}
				retained := journalState(t, a)
				if f.writes != 1 || retained.Operation == nil || retained.Operation.Phase != "Unknown" || len(retained.ActionApplications) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("ambiguous reset/failed readback settled or replayed")
				}
				if (retained.Operation.TaskID == "") != (mode == "lost-ack") {
					t.Fatal("invented or erased original task identity")
				}
				return
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				continue
			}
			if mode == "already-clear" {
				if f.writes != 0 || len(state.ActionApplications) != 0 || state.Operation != nil {
					t.Fatal("matching unowned reset minted effects or custody")
				}
				return
			}
			records, err := networkSettingApplicationRecords(state)
			if err != nil || len(records) != 1 || state.Operation != nil || !recovered || f.writes != phase+1 {
				t.Fatal("reset did not retain one original latest application", err)
			}
			for _, row := range records {
				if row.Operation.TargetID != "area" || row.Operation.TaskID == previousTask || row.Operation.ConfigGeneration != config.Generation {
					t.Fatal("reset changed target or task/source lineage")
				}
				previousTask = row.Operation.TaskID
			}
			if empty {
				fields := f.body[kind].(map[string]any)
				if value, present := fields["servers"]; !present || value != nil {
					t.Fatal("empty list did not emit native null reset")
				}
			}
			for range 2 {
				tick()
			}
			if f.writes != phase+1 || journalState(t, a).Operation != nil {
				t.Fatal("converged reset replayed")
			}
			complete = true
			break
		}
		if !complete {
			t.Fatal("reset did not converge")
		}
	}
}

func TestNetworkEmptyPublicLifecycle(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp"} {
		for _, mode := range []string{"complete", "parent-nonempty", "inherited", "preflight-race", "lost-ack", "native-race", "malformed-read", "already-clear"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseNetworkEmpty(t, kind, mode, nil) })
		}
	}
}

func TestNetworkEmptyConvergenceBoundaries(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "banner", "timezone"} {
		empty := networkSetting{kind: kind, domain: "example.test", bannerType: "Custom", servers: []string{}}
		if (len(networkSettingDrift("Global/Lab", empty, networkObservation{unset: true})) == 0) != (kind == "ntp" || kind == "dhcp") {
			t.Fatal("unset convergence escaped qualified NTP/DHCP empty intents")
		}
		if kind == "ntp" || kind == "dhcp" {
			nonempty := empty
			nonempty.servers = []string{"192.0.2.1"}
			if len(networkSettingDrift("Global/Lab", nonempty, networkObservation{unset: true})) == 0 {
				t.Fatal("unset matched nonempty desired servers")
			}
		}
	}
}
