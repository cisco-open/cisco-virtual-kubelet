// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestOwnedPnPClaimRecoveryObservation(t *testing.T) {
	for _, mode := range []string{"verifying", "legacy-acknowledged", "unknown-binding-version", "malformed-before-binding", "acknowledged-state-transition", "image-outcome-match", "image-outcome-pending", "image-outcome-unreachable", "workflow-image-mismatch", "workflow-image-match", "workflow-image-missing", "workflow-template-match", "workflow-template-mismatch", "workflow-template-missing", "image-binding-changed", "late-image-binding", "template-binding-changed", "wrong-binding", "unknown", "missing-registration", "foreign-owner", "pruned-serial", "wrong-operation", "changed-source", "late-source", "late-owner", "late-inventory", "late-workflow-image", "late-workflow-config", "late-workflow-time", "late-workflow-task", "late-work-item-state", "late-work-item-time"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
			if mode == "image-binding-changed" || mode == "late-image-binding" || (strings.HasPrefix(mode, "image-outcome-") || strings.HasPrefix(mode, "workflow-image-")) {
				want.ImageID = "image-id"
				skip := false
				want.ImageSkip = &skip
			}
			if mode == "template-binding-changed" || strings.HasPrefix(mode, "workflow-template-") {
				want.Template = &pnpTemplateIntent{Name: "Lab#bootstrap"}
			}

			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []pnpDeviceIntent{want}}}})
			index, err := nacmodel.IndexTemplates(raw)
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw = raw
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			reading, changed := false, false
			inventoryReads := 0
			pnpReads := 0
			imageReads := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("recovery observation mutated native state")
					w.WriteHeader(400)
					return
				}
				if reading && !changed && (mode == "late-source" || mode == "late-owner") {
					changed = true
					if mode == "late-source" {
						copy := config.DeepCopy()
						copy.Generation++
						if err := a.statusWriter.Update(ctx, copy); err != nil {
							t.Error(err)
						}
					} else {
						nc, _ := a.currentController(ctx)
						nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Error(err)
						}
					}
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					row := pnpFixture("owned-id", "FOC12345678")
					row["deviceInfo"].(map[string]any)["hostname"] = "edge"
					if reading && mode == "acknowledged-state-transition" {
						row["deviceInfo"].(map[string]any)["state"] = "native-post-claim-state"
					}
					if strings.HasPrefix(mode, "late-work-item-") {
						row["workflow"].(map[string]any)["tasks"] = []any{map[string]any{"state": "unchanged-parent", "workItemList": []any{map[string]any{"state": "unchanged-item", "startTime": 1, "endTime": 2}}}}
					}
					if reading {
						pnpReads++
						switch mode {
						case "workflow-image-match":
							row["workflow"].(map[string]any)["imageId"] = "image-id"
						case "workflow-image-mismatch":
							row["workflow"].(map[string]any)["imageId"] = "foreign-image"
						case "workflow-template-match":
							row["workflow"].(map[string]any)["configId"] = "base-id"
						case "workflow-template-mismatch":
							row["workflow"].(map[string]any)["configId"] = "foreign-template"
						}
						if pnpReads > 1 {
							workflow := row["workflow"].(map[string]any)
							switch mode {
							case "late-work-item-state", "late-work-item-time":
								item := workflow["tasks"].([]any)[0].(map[string]any)["workItemList"].([]any)[0].(map[string]any)
								if mode == "late-work-item-state" {
									item["state"] = "changed-item"
								} else {
									item["endTime"] = 3
								}
							case "late-workflow-image":
								workflow["imageId"] = "new-image"
							case "late-workflow-config":
								workflow["configId"] = "new-config"
							case "late-workflow-time":
								workflow["lastupdateOn"] = 1000
							case "late-workflow-task":
								workflow["tasks"] = []any{map[string]any{"state": "changed", "taskSeqNo": 0}}
							}
						}
					}
					json.NewEncoder(w).Encode([]any{row})
				case imagesPath:
					version := "17.18.04"
					if reading {
						imageReads++
						if mode == "image-binding-changed" || mode == "late-image-binding" && imageReads > 1 {
							version = "26.02.01"
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []Image{{ID: "image-id", Name: "image.bin", Version: version}}})
				case templateProjectsPath:
					json.NewEncoder(w).Encode([]templateProject{{ID: "project-id", Name: "Lab"}})
				case templatesPath:
					json.NewEncoder(w).Encode([]templateSummary{{ID: "base-id", Name: "bootstrap", ProjectID: "project-id"}})
				case templateVersionsPath + "/base-id":
					id, version := "committed-id", 1
					if reading && mode == "template-binding-changed" {
						id = "new-version"
						version = 2
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": "base-id", "versionsInfo": []any{map[string]any{"id": id, "version": version}}}})
				case templatesPath + "/committed-id", templatesPath + "/new-version":
					id, version := "committed-id", 1
					if reading && mode == "template-binding-changed" {
						id = "new-version"
						version = 2
					}
					json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "bootstrap", "projectId": "project-id", "version": version, "composite": false})
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case devicesPath:
					rows := []Device{}
					if reading {
						inventoryReads++
						id := "network-id"
						if mode == "late-inventory" && inventoryReads > 1 {
							id = "replaced-id"
						}
						device := Device{ID: id, Serial: "FOC12345678", Hostname: "edge"}
						if strings.HasPrefix(mode, "image-outcome-") {
							device.SoftwareVersion = "17.18.04"
							device.Reachability = "Reachable"
						}
						if mode == "image-outcome-pending" {
							device.SoftwareVersion = "26.02.01"
						}
						if mode == "image-outcome-unreachable" {
							device.Reachability = "Unreachable"
						}
						rows = append(rows, device)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case assignedDevicesPath:
					fmt.Fprint(w, `{"response":[{"deviceId":"network-id","siteId":"building"}]}`)
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			body, _ := pnpRegistrationPayload(want)
			registration := &configurationWrite{resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), action: "Create", submitCreated: func(context.Context) (string, error) { return "owned-id", nil }}
			if err := a.submitConfigurationWrite(ctx, config, nil, "inventory", registration); err != nil {
				t.Fatal(err)
			}
			id := journalState(t, a).Operation.ID
			if err := a.recordOwnedPnPRegistration(ctx, config, nil, want, id); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, id); err != nil {
				t.Fatal(err)
			}
			plan, err := a.client.planPnPSiteClaim(ctx, want, "owned-id", index)
			if err != nil {
				t.Fatal(err)
			}
			// Reconstruct an acknowledged operation through the shared journal;
			// native submission/admission sequencing has separate integration tests.
			identity := configurationResourceIdentity{target: "owned-id", parent: "building", serial: "foc12345678"}
			op, err := a.prepareConfiguration(ctx, config, nil, "inventory", plan.resource, "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			if err := a.recordConfigurationCompleted(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
			}
			nc, _ := a.currentController(ctx)
			switch mode {
			case "legacy-acknowledged":
				parts := strings.Split(nc.Status.Configuration.Operation.Resource, ":")
				nc.Status.Configuration.Operation.Resource = "pnp-claim:" + parts[2]
			case "unknown-binding-version":
				nc.Status.Configuration.Operation.Resource = strings.Replace(nc.Status.Configuration.Operation.Resource, ":v2:", ":v9:", 1)
			case "malformed-before-binding":
				nc.Status.Configuration.Operation.Resource = nc.Status.Configuration.Operation.Resource[:len(nc.Status.Configuration.Operation.Resource)-1]
			case "wrong-binding":
				nc.Status.Configuration.Operation.Resource = "pnp-claim:" + strings.Repeat("0", 64)
			case "missing-registration":
				nc.Status.Configuration.ActionApplications = nil
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "pruned-serial":
				nc.Status.Configuration.Operation.TargetSerial = ""
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			op = journalState(t, a).Operation.DeepCopy()
			if mode == "wrong-operation" {
				op.ID = "00000000000000000000000000000000"
			}
			if mode == "changed-source" {
				config = config.DeepCopy()
				config.Generation++
			}
			beforeObservation := journalState(t, a).DeepCopy()
			reading = true
			got, err := a.observeOwnedPnPClaimInventory(ctx, config, nil, want, op, index)
			valid := mode == "verifying" || mode == "legacy-acknowledged" || mode == "acknowledged-state-transition" || mode == "unknown" || strings.HasPrefix(mode, "image-outcome-") || mode == "workflow-image-match" || mode == "workflow-image-missing" || mode == "workflow-template-match" || mode == "workflow-template-missing"
			if (err == nil) != valid {
				t.Fatal("recovery observation result", err)
			}
			if valid && (got == nil || got.snapshot == nil || got.snapshot.inventory == nil || got.snapshot.inventory.ID != "network-id" || !got.snapshot.assigned) {
				t.Fatal("lost inventory evidence")
			}
			if valid {
				expected := "Skipped"
				switch mode {
				case "image-outcome-match":
					expected = "Matched"
				case "image-outcome-pending":
					expected = "PendingVersion"
				case "workflow-image-match", "workflow-image-missing":
					expected = "PendingReachability"
				case "image-outcome-unreachable":
					expected = "PendingReachability"
				}
				workflowExpected := "NotRequested"
				if strings.HasPrefix(mode, "image-outcome-") || mode == "workflow-image-missing" {
					workflowExpected = "PendingImageReference"
				}
				if mode == "workflow-image-match" || mode == "workflow-template-match" {
					workflowExpected = "MatchedReferences"
				}
				if mode == "workflow-template-missing" {
					workflowExpected = "PendingTemplateReference"
				}
				if got.workflow.state != workflowExpected {
					t.Fatal("wrong owned workflow reference evidence", got.workflow)
				}
				if got.image.state != expected {
					t.Fatal("wrong owned image observation", got.image)
				}
			}
			if !valid && got != nil {
				t.Fatal("exposed unowned/stale evidence")
			}
			afterObservation := journalState(t, a)
			if !reflect.DeepEqual(beforeObservation.Operation, afterObservation.Operation) || !reflect.DeepEqual(beforeObservation.ActionApplications, afterObservation.ActionApplications) {
				t.Fatal("read-only recovery changed the operation or application receipts")
			}
			phase := "Verifying"
			if mode == "unknown" {
				phase = "Unknown"
			}
			if journalState(t, a).Operation.Phase != phase {
				t.Fatal("observation advanced completion")
			}
		})
	}
}
