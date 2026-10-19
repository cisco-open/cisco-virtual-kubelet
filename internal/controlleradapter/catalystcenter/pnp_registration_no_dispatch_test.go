// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPnPRegistrationNoDispatchReplan(t *testing.T) {
	for _, mode := range []string{"read-error", "cohort", "source", "configmap", "owner", "operation", "inner-read-error", "inner-cohort", "inner-owner", "inner-operation", "inner-managed", "inner-network-error"} {
		t.Run(mode, func(t *testing.T) { exercisePnPRegistrationNoDispatch(t, mode, nil) })
	}
}

func exercisePnPRegistrationNoDispatch(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	baseMode := strings.TrimPrefix(mode, "inner-")
	activeReads := 0
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.Source.Inline.Raw = []byte(pnpIntentFixture())
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	var cm *core.ConfigMap
	wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
	if err != nil {
		t.Fatal(err)
	}
	armed, refused, posts := true, false, 0
	injectNetwork := true
	rows := []map[string]any{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == pnpDevicesPath:
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if armed && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
				activeReads++
				if strings.HasPrefix(mode, "inner-") && activeReads < 3 {
					json.NewEncoder(w).Encode(rows)
					return
				}
				armed = false
				refused = true
				switch baseMode {
				case "read-error":
					w.WriteHeader(503)
					return
				case "cohort":
					json.NewEncoder(w).Encode([]map[string]any{pnpFixture("foreign", wants[0].Serial)})
					return
				case "source":
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
						t.Error(err)
					}
					config.Spec.ModelSource.Exporter = "changed-source"
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Error(err)
					}
				case "configmap":
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
						t.Error(err)
					}
					cm.Data["model"] += " "
					if err := a.statusWriter.Update(ctx, cm); err != nil {
						t.Error(err)
					}
				case "owner":
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign-owner"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				case "operation":
					nc.Status.Configuration.Operation.SourceVersion = "foreign-version"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				}
				if strings.HasPrefix(mode, "inner-") && (baseMode == "owner" || baseMode == "operation") {
					w.WriteHeader(503)
					return
				}
			}
			json.NewEncoder(w).Encode(rows)
		case r.Method == http.MethodGet && r.URL.Path == devicesPath:
			if injectNetwork && refused && baseMode == "network-error" {
				w.WriteHeader(503)
				return
			}
			if injectNetwork && refused && baseMode == "managed" {
				json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "managed-device", Serial: wants[0].Serial}}})
				return
			}
			fmt.Fprint(w, `{"response":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == pnpDevicesPath:
			posts++
			row := pnpFixture("owned-registration", wants[0].Serial)
			row["deviceInfo"].(map[string]any)["hostname"] = wants[0].Name
			rows = append(rows, row)
			json.NewEncoder(w).Encode(row)
		default:
			t.Error("unsupported API", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	})
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = a.client.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	if mode == "configmap" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "pnp-model", Namespace: config.Namespace}, Data: map[string]string{"model": pnpIntentFixture()}}
		if install == nil {
			cm.UID = "pnp-source"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline = nil
		config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		t.Helper()
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	injectNetwork = false
	state := journalState(t, a)
	if !refused || posts != 0 || state == nil || len(state.Owners) != 1 || len(state.ActionApplications) != 0 {
		t.Fatal("pre-dispatch boundary not exercised", refused, posts, state)
	}
	if strings.HasPrefix(mode, "inner-") && activeReads != 3 {
		t.Fatal("primitive refusal did not follow both whole-cohort observations", activeReads)
	}
	if baseMode == "owner" || baseMode == "operation" {
		if state.Operation == nil {
			t.Fatal("changed authority retired")
		}
		before := state.DeepCopy()
		for range 2 {
			tick()
		}
		if posts != 0 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("uncertain authority replayed")
		}
		return
	}
	if state.Operation != nil || state.Owners[0].ConfigUID != string(config.UID) {
		t.Fatal("proven refusal failed to preserve idle owner", state)
	}
	replan := false
	for _, condition := range config.Status.Conditions {
		if condition.Type == configapi.NetworkControllerConfigConditionReady && condition.Status == metav1.ConditionFalse && condition.Reason == "PnPRegistrationReplanRequired" {
			replan = true
		}
	}
	if mode == "source" || mode == "configmap" {
		// The shared publication fence suppresses results computed against the
		// superseded source. Do not weaken it to publish the new replan reason.
		if len(config.Status.Conditions) != 0 {
			t.Fatal("superseded source published stale conditions", config.Status.Conditions)
		}
	} else if !replan {
		t.Fatal("public status did not distinguish a proven pre-dispatch refusal", config.Status.Conditions)
	}
	// Rebuild around the same store and allow a fresh plan, never the old closure.
	rebuilt := *a
	a = &rebuilt
	for range 8 {
		tick()
	}
	state = journalState(t, a)
	if posts != 1 || state.Operation != nil || len(state.ActionApplications) != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
		t.Fatal("fresh registration failed", posts, state, config.Status)
	}
	for range 2 {
		tick()
	}
	if posts != 1 {
		t.Fatal("owned registration replayed")
	}
}

func TestPnPNoDispatchScope(t *testing.T) {
	base := &configurationWrite{resource: "pnp-register:" + strings.Repeat("a", 64), action: "Create", submitCreated: func(context.Context) (string, error) { return "", nil }, noDispatch: func(error) *configurationPreSubmitRefusal { return nil }}
	for _, mode := range []string{"valid", "uppercase", "short", "claim", "update", "target", "serial", "parent", "task-writer", "completed-writer"} {
		t.Run(mode, func(t *testing.T) {
			w := *base
			switch mode {
			case "uppercase":
				w.resource = strings.ToUpper(w.resource)
			case "short":
				w.resource = "pnp-register:ab"
			case "claim":
				w.resource = "pnp-site-claim:" + strings.Repeat("a", 64)
			case "update":
				w.action = "Update"
			case "target":
				w.identity.target = "id"
			case "serial":
				w.identity.serial = "FOC12345678"
			case "parent":
				w.identity.parent = "parent"
			case "task-writer":
				w.submit = func(context.Context) (Task, error) { return Task{}, nil }
			case "completed-writer":
				w.submitCompleted = func(context.Context) error { return nil }
			}
			if noDispatchWriteSupported("inventory", &w) != (mode == "valid") {
				t.Fatal("incorrect inventory proof scope")
			}
		})
	}
}

// An inner proof belongs to its exact native plan, not merely the same payload.
func TestPnPPrimitiveProofIsolation(t *testing.T) {
	ctx := context.Background()
	refuse := false
	posts := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == http.MethodGet && r.URL.Path == pnpDevicesPath:
			if refuse {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `[]`)
		case r.Method == http.MethodGet && r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == pnpDevicesPath:
			posts++
			w.WriteHeader(409)
		default:
			t.Error("unexpected API", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	})
	wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.planPnPRegistration(ctx, wants[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.planPnPRegistration(ctx, wants[0])
	if err != nil {
		t.Fatal(err)
	}
	refuse = true
	_, err = first.submit(ctx)
	if err == nil || second.noDispatch(err) != nil || first.noDispatch(err) == nil || first.noDispatch(err) != nil {
		t.Fatal("foreign or spent primitive proof accepted")
	}
	_, again := first.submit(ctx)
	if again == nil || first.noDispatch(again) != nil || posts != 0 {
		t.Fatal("consumed primitive plan replayed")
	}
	refuse = false
	_, err = second.submit(ctx)
	if err == nil || second.noDispatch(err) != nil || posts != 1 {
		t.Fatal("post-entry HTTP conflict acquired no-dispatch authority")
	}
}
