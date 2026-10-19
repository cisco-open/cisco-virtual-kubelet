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

func telemetryPartialModel(t *testing.T, fields map[string]any, path string) []byte {
	t.Helper()
	profile := map[string]any{"name": "base"}
	for k, v := range fields {
		profile[k] = v
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{"areas": []any{map[string]any{"name": lastSiteName(path), "parent_name": parentSitePath(path), "network_settings": map[string]any{"telemetry": "base"}}}}, "network_settings": map[string]any{"telemetry": []any{profile}}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func exerciseTelemetryPartial(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	e, f := networkSubmissionFixtureExecutor(t, "telemetry")
	a.client = e.client
	var expected map[string]any
	if err := json.Unmarshal([]byte(telemetryResponse), &expected); err != nil {
		t.Fatal(err)
	}
	var initial map[string]any
	_ = json.Unmarshal([]byte(telemetryResponse), &initial)
	f.data["area/telemetry"] = initial
	fields := map[string]any{"wired_data_collection": true}
	switch mode {
	case "syslog":
		fields = map[string]any{"catalyst_center_as_syslog_server": true}
		expected["syslogs"].(map[string]any)["useBuiltinSyslogServer"] = true
	case "snmp":
		fields = map[string]any{"snmp_servers": []string{"192.0.2.99"}}
		expected["snmpTraps"].(map[string]any)["externalTrapServers"] = []string{"192.0.2.99"}
	case "enable":
		fields = map[string]any{"enable_netflow_collector_on_devices": false}
		expected["applicationVisibility"].(map[string]any)["enableOnWiredAccessDevices"] = false
	case "builtin":
		fields = map[string]any{"catalyst_center_as_network_collector": true}
		expected["applicationVisibility"].(map[string]any)["collector"] = map[string]any{"collectorType": "Builtin"}
	case "null":
		fields["wireless_telemetry"] = nil
		expected["wiredDataCollection"].(map[string]any)["enableWiredDataCollection"] = true
	case "noop":
		fields = map[string]any{"wired_data_collection": false}
	case "all-null":
		fields = map[string]any{"wired_data_collection": nil, "catalyst_center_as_network_collector": nil}
	default:
		expected["wiredDataCollection"].(map[string]any)["enableWiredDataCollection"] = true
	}
	if mode == "unknown-native" {
		f.data["area/telemetry"]["wiredDataCollection"].(map[string]any)["futureFlag"] = true
	}
	if mode == "missing" {
		f.data = map[string]map[string]any{}
	}
	if mode == "lost-ack" {
		f.lostAck = true
	}
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: telemetryPartialModel(t, fields, "Global/Lab")}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	injected := false
	if mode == "race" {
		base := a.statusWriter
		a.statusWriter = networkSubmittingClient{Client: base, after: func() {
			if injected {
				return
			}
			injected = true
			f.mu.Lock()
			defer f.mu.Unlock()
			f.data["area/telemetry"]["wirelessTelemetry"].(map[string]any)["enableWirelessTelemetry"] = false
			expected["wirelessTelemetry"].(map[string]any)["enableWirelessTelemetry"] = false
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
	recovered := false
	for step := 0; step < 30; step++ {
		tick()
		state := journalState(t, a)
		if mode == "race" && step == 0 && (!injected || f.writes != 0 || state.Operation != nil) {
			t.Fatal("unselected preserved-field race dispatched")
		}
		if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered {
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			recovered = true
			if mode == "source-null" {
				config.Spec.Source.Inline.Raw = telemetryPartialModel(t, map[string]any{"wired_data_collection": nil}, "Global/Lab")
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
		}
		if mode == "lost-ack" || mode == "missing" || mode == "source-null" || mode == "unknown-native" {
			if step < 6 {
				continue
			}
			if mode == "source-null" {
				if state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID == "" || f.writes != 1 || len(state.ActionApplications) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("null source revision cleared original acknowledged telemetry fence")
				}
				return
			}
			if mode == "missing" || mode == "unknown-native" {
				if f.writes != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("missing preserved fields invented defaults")
				}
			} else if state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" || f.writes != 1 || len(state.ActionApplications) != 0 {
				t.Fatal("lost acknowledgement replayed or settled")
			}
			return
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			continue
		}
		wantWrites := 1
		if mode == "noop" || mode == "all-null" {
			wantWrites = 0
		}
		if f.writes != wantWrites || (wantWrites == 1 && !recovered) {
			t.Fatal("wrong task/dispatch count")
		}
		records, err := networkSettingApplicationRecords(state)
		if err != nil || len(records) != wantWrites || state.Operation != nil {
			t.Fatal("invalid latest receipt lifecycle", err)
		}
		got, _ := json.Marshal(f.data["area/telemetry"])
		want, _ := json.Marshal(expected)
		if string(got) != string(want) {
			t.Fatal("partial telemetry lost unselected native fields")
		}
		for range 2 {
			tick()
		}
		if f.writes != wantWrites || journalState(t, a).Operation != nil {
			t.Fatal("partial telemetry no-op replayed")
		}
		return
	}
	t.Fatal("partial telemetry did not converge")
}

func TestTelemetryPartialPublic(t *testing.T) {
	for _, mode := range []string{"wired", "syslog", "snmp", "enable", "builtin", "null", "noop", "all-null", "missing", "race", "lost-ack", "source-null", "unknown-native"} {
		t.Run(mode, func(t *testing.T) { exerciseTelemetryPartial(t, mode, nil) })
	}
}
