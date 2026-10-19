// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPnPRegistrationStageLifecycle(t *testing.T) {
	for _, mode := range []string{"cycle", "stack-registration", "configmap", "late-configmap", "later-existing", "later-managed", "later-managed-stack", "later-invalid", "late-cohort", "last-cohort", "lost", "async", "replaced", "pruned-uuid", "missing-receipt", "foreign-operation", "prepared", "prepared-receipt", "prepared-target", "foreign-parent", "changed-source", "late-source", "later-revision", "report", "delete"} {
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
			if mode == "stack-registration" {
				wants[0].Serial += ",FOC12345680"
			}
			if mode == "later-invalid" {
				wants[1].PID = ""
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": wants}}})
			config.Spec.Source.Inline.Raw = raw
			config.Spec.ManagedSections = []string{"inventory"}
			if mode == "report" {
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			}
			if mode == "delete" {
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			var source *configSourceSnapshot
			var sourceMap *corev1.ConfigMap
			if mode == "configmap" || mode == "late-configmap" {
				sourceMap = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "pnp-model", UID: "pnp-source"}, Data: map[string]string{"model": string(raw)}}
				if err := a.statusWriter.Create(ctx, sourceMap); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: sourceMap.Name, Key: "model"}
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if sourceMap != nil {
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
			}
			rows := []map[string]any{}
			posts, pnpReads, networkReads := 0, 0, 0
			late := false
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch {
				case r.Method == "GET" && r.URL.Path == pnpDevicesPath:
					pnpReads++
					extra := mode == "later-existing" || mode == "late-cohort" && pnpReads >= 2 || mode == "last-cohort" && pnpReads >= 5
					out := append([]map[string]any{}, rows...)
					if extra {
						out = append(out, pnpFixture("foreign-id", wants[1].Serial))
					}
					if late && mode == "late-configmap" {
						late = false
						sourceMap.Data["model"] = string(raw) + " "
						if err := a.statusWriter.Update(ctx, sourceMap); err != nil {
							t.Error(err)
						}
					}
					if late && mode == "late-source" {
						late = false
						copy := config.DeepCopy()
						copy.Generation++
						if err := a.statusWriter.Update(ctx, copy); err != nil {
							t.Error(err)
						}
					}
					json.NewEncoder(w).Encode(out)
				case r.Method == "GET" && r.URL.Path == devicesPath:
					networkReads++
					inventory := []Device{}
					if mode == "later-managed" {
						inventory = append(inventory, Device{ID: "managed", Serial: wants[1].Serial})
					}
					if mode == "later-managed-stack" {
						inventory = append(inventory, Device{ID: "managed", Serial: "OTHER123456," + wants[1].Serial})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": inventory})
				case r.Method == "POST" && r.URL.Path == pnpDevicesPath:
					posts++
					var request struct {
						Info pnpDeviceInfo `json:"deviceInfo"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("invalid registration payload")
					}
					row := pnpFixture(fmt.Sprintf("created-%d", posts), request.Info.Serial)
					row["deviceInfo"].(map[string]any)["hostname"] = request.Info.Hostname
					row["deviceInfo"].(map[string]any)["pid"] = request.Info.PID
					if request.Info.Stack != nil {
						row["deviceInfo"].(map[string]any)["stack"] = *request.Info.Stack
					}
					rows = append(rows, row)
					if mode == "lost" {
						w.WriteHeader(502)
						return
					}
					if mode == "async" {
						w.WriteHeader(202)
						return
					}
					json.NewEncoder(w).Encode(row)
				default:
					t.Error("unexpected API", r.Method, r.URL.Path)
					w.WriteHeader(400)
				}
			})
			if mode == "foreign-operation" || strings.HasPrefix(mode, "prepared") {
				body, _ := pnpRegistrationPayload(wants[0])
				resource := fmt.Sprintf("pnp-register:%x", sha256.Sum256(body))
				if mode == "foreign-operation" {
					resource = "foreign"
				}
				if _, err := a.prepareConfiguration(ctx, config, source, "inventory", resource, "Create"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "prepared-receipt" || mode == "prepared-target" {
				nc, _ := a.currentController(ctx)
				if mode == "prepared-receipt" {
					nc.Status.Configuration.Operation.CreatedResourceID = "unverified"
				} else {
					nc.Status.Configuration.Operation.TargetID = "foreign"
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			done, err := a.reconcilePnPRegistrationStage(ctx, config, source)
			refused := mode == "later-existing" || mode == "later-managed" || mode == "later-managed-stack" || mode == "later-invalid" || mode == "late-cohort" || mode == "last-cohort" || mode == "report" || mode == "delete" || mode == "foreign-operation" || mode == "lost" || mode == "async" || mode == "prepared-receipt" || mode == "prepared-target"
			if (err != nil) != refused || done {
				t.Fatal("initial stage", done, err)
			}
			if refused {
				expected := 0
				if mode == "lost" || mode == "async" {
					expected = 1
				}
				if posts != expected {
					t.Fatal("unexpected registration count", posts)
				}
				if mode == "last-cohort" {
					if !errors.Is(err, errConfigurationNotDispatched) || journalState(t, a).Operation != nil {
						t.Fatal("proven pre-dispatch refusal did not retire", err)
					}
				} else if mode == "lost" || mode == "async" {
					before := journalState(t, a).DeepCopy()
					if before.Operation == nil || before.Operation.Phase != "Unknown" {
						t.Fatal("uncertain registration not retained")
					}
					for range 2 {
						if _, err := a.reconcilePnPRegistrationStage(ctx, config, source); err == nil {
							t.Fatal("uncertain operation replayed")
						}
					}
					if posts != expected || !reflect.DeepEqual(before, journalState(t, a)) {
						t.Fatal("uncertain registration changed")
					}
				} else if mode != "foreign-operation" && mode != "prepared-receipt" && mode != "prepared-target" && journalState(t, a) != nil && journalState(t, a).Operation != nil {
					t.Fatal("preflight created an earlier journal")
				}
				return
			}
			if mode == "prepared" {
				if posts != 0 || journalState(t, a).Operation != nil {
					t.Fatal("prepared work dispatched instead of retired")
				}
				if _, err := a.reconcilePnPRegistrationStage(ctx, config, source); err != nil {
					t.Fatal(err)
				}
			}
			if posts != 1 || journalState(t, a).Operation.Phase != "Verifying" || journalState(t, a).Operation.CreatedResourceID != "created-1" {
				t.Fatal("created identity not persisted")
			}
			// Serialize persistence and rebuild the adapter around the same authoritative store.
			nc, _ := a.currentController(ctx)
			encoded, _ := json.Marshal(nc.Status.Configuration)
			copy := nc.DeepCopy()
			if json.Unmarshal(encoded, &copy.Status.Configuration) != nil {
				t.Fatal("serialization")
			}
			if err := a.statusWriter.Status().Update(ctx, copy); err != nil {
				t.Fatal(err)
			}
			rebuilt := *a
			a = &rebuilt
			switch mode {
			case "replaced":
				rows[0]["id"] = "replacement"
			case "foreign-parent":
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Operation.ParentID = "foreign-site"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "pruned-uuid":
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Operation.CreatedResourceID = ""
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "changed-source":
				config = config.DeepCopy()
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "late-source", "late-configmap":
				late = true
			}
			_, err = a.reconcilePnPRegistrationStage(ctx, config, source)
			invalid := mode == "replaced" || mode == "pruned-uuid" || mode == "changed-source" || mode == "late-source" || mode == "late-configmap" || mode == "foreign-parent"
			if (err != nil) != invalid {
				t.Fatal("recovery result", err)
			}
			if invalid {
				if posts != 1 || journalState(t, a).Operation == nil {
					t.Fatal("failed recovery lost its journal")
				}
				return
			}
			if journalState(t, a).Operation.Phase != "Verified" || len(journalState(t, a).ActionApplications) != 1 {
				t.Fatal("ownership not atomic")
			}
			if mode == "missing-receipt" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.ActionApplications = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if _, err := a.reconcilePnPRegistrationStage(ctx, config, source); err == nil {
					t.Fatal("unowned verified operation cleared")
				}
				return
			}
			for step := 0; step < 12; step++ {
				done, err = a.reconcilePnPRegistrationStage(ctx, config, source)
				if err != nil {
					t.Fatal("remaining cohort", err)
				}
				if done {
					break
				}
			}
			if !done || posts != 2 || journalState(t, a).Operation != nil || len(journalState(t, a).ActionApplications) != 2 {
				t.Fatal("cohort not registered")
			}
			if mode == "later-revision" {
				config = config.DeepCopy()
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(raw), "image.bin", "next-image.bin", -1))
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				done, err = a.reconcilePnPRegistrationStage(ctx, config, source)
				if err != nil || !done {
					t.Fatal("owned no-op", err)
				}
			}
			if posts != 2 {
				t.Fatal("owned records recreated")
			}
			if networkReads == 0 {
				t.Fatal("network inventory not checked")
			}
		})
	}
}

func TestPnPRegistrationCohortInventoryReadBudget(t *testing.T) {
	for _, count := range []int{256, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			wants := make([]pnpDeviceIntent, count)
			for i := range wants {
				wants[i] = pnpDeviceIntent{Name: fmt.Sprintf("edge-%d", i), State: "PNP", Serial: fmt.Sprintf("FOC%08d", i), PID: "C9300-48P", Site: "Global/Lab/Building"}
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": wants}}})
			config.Spec.Source.Inline.Raw = raw
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			pnpReads, networkReads := 0, 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("cohort observation mutated native state")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					pnpReads++
					fmt.Fprint(w, `[]`)
				case devicesPath:
					networkReads++
					fmt.Fprint(w, `{"response":[]}`)
				default:
					t.Error("unexpected API")
					w.WriteHeader(400)
				}
			})
			got, err := a.observePnPRegistrationCohort(ctx, config, nil, wants, nil)
			if pnpReads != 1 || networkReads != 1 {
				t.Fatal("cohort did not share complete native observations", pnpReads, networkReads)
			}
			if count == 1000 {
				if !errors.Is(err, errPnPRegistrationCapacity) || got != nil {
					t.Fatal("oversized cohort bypassed receipt admission", err)
				}
			} else if err != nil || len(got) != count {
				t.Fatal("bounded cohort refused", len(got), err)
			}
		})
	}
}
