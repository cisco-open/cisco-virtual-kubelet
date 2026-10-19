// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
	"strings"
	"testing"
)

func TestPnPRegistrationPublicReconciler(t *testing.T) {
	for _, mode := range []string{"inline", "example", "configmap", "lost", "unowned-later", "managed-later", "mixed-state", "unknown-later", "extra-section", "report", "delete", "wrong-version", "pruned-source", "pruned-uuid", "late-source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			second := wants[0]
			second.Name = "edge2"
			second.Serial = "FOC12345679"
			wants = append(wants, second)
			root := map[string]any{"inventory": map[string]any{"devices": wants}}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": root})
			switch mode {
			case "example":
				data, err := os.ReadFile("../../../examples/configs/catalyst-center/pnp-registration-execution.yaml")
				if err != nil {
					t.Fatal(err)
				}
				var example configapi.NetworkControllerConfig
				if err := yaml.UnmarshalStrict(data, &example); err != nil {
					t.Fatal(err)
				}
				config.Spec = example.Spec
				raw = example.Spec.Source.Inline.Raw
				wants, err = parsePnPInventory(raw)
				if err != nil || len(wants) != 2 {
					t.Fatal("invalid example cohort", err)
				}
			case "mixed-state":
				raw = []byte(strings.Replace(string(raw), `"state":"PNP"`, `"state":"PROVISION"`, 1))
			case "unknown-later":
				raw = []byte(strings.Replace(string(raw), `"name":"edge2"`, `"unknown":"secret-field","name":"edge2"`, 1))
			case "extra-section":
				root["sites"] = map[string]any{"areas": []any{map[string]any{"name": "bad"}}}
				raw, _ = json.Marshal(map[string]any{"catalyst_center": root})
			case "report":
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "delete":
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "wrong-version":
				config.Spec.ModelSource.ModelVersion = "0.6.0"
			}
			config.Spec.Source.Inline.Raw = raw
			config.Spec.ManagedSections = []string{"inventory"}
			if mode == "configmap" {
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "pnp", Namespace: config.Namespace, UID: "pnp-model"}, Data: map[string]string{"model": string(raw)}}
				if err := a.statusWriter.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			posts := 0
			rows := []map[string]any{}
			late := false
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch {
				case r.Method == "GET" && r.URL.Path == pnpDevicesPath:
					if mode == "late-source" && late {
						late = false
						copy := config.DeepCopy()
						copy.Generation++
						if err := a.statusWriter.Update(ctx, copy); err != nil {
							t.Error(err)
						}
					}
					out := append([]map[string]any{}, rows...)
					if mode == "unowned-later" {
						out = append(out, pnpFixture("foreign", second.Serial))
					}
					json.NewEncoder(w).Encode(out)
				case r.Method == "GET" && r.URL.Path == devicesPath:
					inventory := []Device{}
					if mode == "managed-later" {
						inventory = append(inventory, Device{ID: "managed", Serial: second.Serial})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": inventory})
				case r.Method == "POST" && r.URL.Path == pnpDevicesPath:
					posts++
					var request struct {
						Info pnpDeviceInfo `json:"deviceInfo"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("invalid registration")
					}
					row := pnpFixture(fmt.Sprintf("record-%d", posts), request.Info.Serial)
					row["deviceInfo"].(map[string]any)["hostname"] = request.Info.Hostname
					rows = append(rows, row)
					if mode == "lost" {
						w.WriteHeader(502)
						return
					}
					json.NewEncoder(w).Encode(row)
				default:
					t.Error("public registration invoked unsupported API", r.Method, r.URL.Path)
					w.WriteHeader(400)
				}
			})
			nc, _ := a.currentController(ctx)
			nc.Spec.Endpoint = a.client.cfg.Endpoint
			if err := a.statusWriter.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			request := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
			reconcile := func() {
				t.Helper()
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, request); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, request.NamespacedName, config); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(config.Status)
				if strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "secret-field") {
					t.Fatal("source secrets escaped to status")
				}
				if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("registration published onboarding InSync")
				}
				for _, c := range config.Status.Conditions {
					if c.Type == configapi.NetworkControllerConfigConditionReady && c.Status == metav1.ConditionTrue {
						t.Fatal("registration published Ready")
					}
				}
			}
			reconcile()
			refused := mode == "unowned-later" || mode == "managed-later" || mode == "mixed-state" || mode == "unknown-later" || mode == "extra-section" || mode == "report" || mode == "delete" || mode == "wrong-version"
			if refused {
				if posts != 0 || (journalState(t, a) != nil && journalState(t, a).Operation != nil) {
					t.Fatal("unsupported declaration caused a write/journal")
				}
				return
			}
			if posts != 1 || journalState(t, a).Operation == nil {
				t.Fatal("public route did not submit registration", config.Status)
			}
			if mode == "lost" {
				before := journalState(t, a).DeepCopy()
				for range 2 {
					reconcile()
				}
				if posts != 1 || !reflect.DeepEqual(before, journalState(t, a)) || before.Operation.Phase != "Unknown" {
					t.Fatal("lost registration was replayed")
				}
				return
			}
			if mode == "pruned-source" {
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "pruned-uuid" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Operation.CreatedResourceID = ""
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "late-source" {
				late = true
			}
			if mode == "pruned-source" || mode == "pruned-uuid" || mode == "late-source" {
				before := journalState(t, a).DeepCopy()
				reconcile()
				if posts != 1 || !reflect.DeepEqual(before, journalState(t, a)) {
					t.Fatal("retained work escaped through generic recovery")
				}
				return
			}
			for step := 0; step < 10; step++ {
				nc, _ := a.currentController(ctx)
				raw, _ := json.Marshal(nc.Status.Configuration)
				copy := nc.DeepCopy()
				if err := json.Unmarshal(raw, &copy.Status.Configuration); err != nil {
					t.Fatal(err)
				}
				if err := a.statusWriter.Status().Update(ctx, copy); err != nil {
					t.Fatal(err)
				}
				rebuilt := *a
				a = &rebuilt
				reconcile()
			}
			state := journalState(t, a)
			if posts != 2 || state.Operation != nil || len(state.ActionApplications) != 2 || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
				t.Fatal("public registration stage did not converge", config.Status)
			}
			found := false
			for _, c := range config.Status.Conditions {
				if c.Type == configapi.NetworkControllerConfigConditionReady && c.Reason == "PnPClaimAdmissionUnavailable" {
					found = true
				}
			}
			if !found {
				t.Fatal("pending claim gate not exposed", config.Status)
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("registration manufactured physical admission", err)
			}
		})
	}
}
