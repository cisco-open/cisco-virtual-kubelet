// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func networkSingleSettingModel(t *testing.T, kind string) []byte {
	t.Helper()
	var model map[string]any
	if json.Unmarshal(allNetworkExecutionModel(), &model) != nil {
		t.Fatal("model")
	}
	root := model["catalyst_center"].(map[string]any)
	settings := root["network_settings"].(map[string]any)
	category := "network"
	if kind == "telemetry" {
		category = "telemetry"
		delete(settings, "network")
	} else {
		delete(settings, "telemetry")
		profile := settings["network"].([]any)[0].(map[string]any)
		allowed := map[string]bool{"name": true}
		switch kind {
		case "ntp", "dhcp":
			allowed[kind+"_servers"] = true
		case "dns":
			allowed["domain_name"], allowed["dns_servers"] = true, true
		default:
			allowed[kind] = true
		}
		for key := range profile {
			if !allowed[key] {
				delete(profile, key)
			}
		}
	}
	root["sites"] = map[string]any{"areas": []any{map[string]any{"name": "Lab", "parent_name": "Global", "network_settings": map[string]any{category: "base"}}}}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func exerciseNetworkSubmissionRecovery(t *testing.T, kind, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
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
	injected := false
	if mode == "lost-ack" {
		f.lostAck = true
	}
	if mode == "read-error" || mode == "stale" {
		a.statusWriter = networkSubmittingClient{Client: a.statusWriter, after: func() {
			if injected {
				return
			}
			injected = true
			f.mu.Lock()
			defer f.mu.Unlock()
			if mode == "read-error" {
				f.failRead = true
			} else {
				f.data["area/"+networkFixtureAPIKind(kind)] = networkFixturePayload(t, e.bindings[0].settings[0])
			}
		}}
	}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	for step := 0; step < 12; step++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
		if step == 0 && (mode == "read-error" || mode == "stale") {
			state := journalState(t, a)
			if !injected || state.Operation != nil || len(state.Owners) != 1 || len(state.ActionApplications) != 0 {
				t.Fatal("proven no-dispatch operation was not retired")
			}
			found := false
			for _, condition := range config.Status.Conditions {
				if condition.Reason == "NativePreflightRefused" {
					found = true
				}
			}
			if !found {
				t.Fatal("public refusal reason absent")
			}
			f.mu.Lock()
			f.failRead = false
			if f.writes != 0 {
				t.Fatal("preflight sent mutation")
			}
			f.mu.Unlock()
		}
	}
	state := journalState(t, a)
	if mode == "lost-ack" {
		if state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" {
			t.Fatal("ambiguous request was not retained")
		}
	} else if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil || len(state.Owners) != 1 {
		t.Fatal("public settings did not converge after refusal")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := 1
	if mode == "stale" {
		writes = 0
	}
	if f.writes != writes {
		t.Fatal("wrong native request count", f.writes)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Authorized lab access") {
		t.Fatal("private banner retained in journal")
	}
}
func TestNetworkSubmissionPublicRecovery(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"complete", "read-error", "stale", "lost-ack"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseNetworkSubmissionRecovery(t, kind, mode, nil) })
		}
	}
}

type networkSubmittingClient struct {
	ctrlclient.Client
	after func()
}
type networkSubmittingWriter struct {
	ctrlclient.SubResourceWriter
	after func()
}

func (c networkSubmittingClient) Status() ctrlclient.SubResourceWriter {
	return networkSubmittingWriter{c.Client.Status(), c.after}
}
func (w networkSubmittingWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
		w.after()
	}
	return nil
}
