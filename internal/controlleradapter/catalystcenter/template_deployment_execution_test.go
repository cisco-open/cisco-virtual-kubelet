// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func TestRegularTemplateSubmission(t *testing.T) {
	for _, mode := range []string{"accepted", "accepted-empty-selection", "accepted-binding", "lost-receipt", "rejected", "caller-change", "late-version", "late-platform", "late-content", "late-empty-selection", "late-binding", "recover-success", "recover-empty-selection", "recover-binding", "recover-new-latest", "recover-pending", "recover-failed"} {
		t.Run(mode, func(t *testing.T) {
			emptySelection := strings.Contains(mode, "empty-selection")
			binding := strings.HasSuffix(mode, "-binding")
			omittedParameters := emptySelection || binding
			late := false
			posts := 0
			provisioned := true
			var duringRead func()
			versions := 0
			now := time.Now().UTC().Truncate(time.Second)
			submitted := metav1.NewTime(now.Add(-time.Minute))
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == templateDeployPath && r.Method == "POST" {
					posts++
					var payload map[string]any
					if json.NewDecoder(r.Body).Decode(&payload) != nil {
						t.Error("invalid payload")
					}
					targets, ok := payload["targetInfo"].([]any)
					if !ok || len(targets) != 1 {
						t.Error("wrong target count")
						w.WriteHeader(500)
						return
					}
					target := targets[0].(map[string]any)
					params := target["params"].(map[string]any)
					if payload["templateId"] != "base" || target["id"] != "device" || target["versionedTemplateId"] != "committed" || !omittedParameters && params["value"] != "secret-original" || omittedParameters && len(params) != 0 {
						t.Error("planned payload changed")
					}
					if binding {
						encoded, _ := json.Marshal(payload)
						if strings.Contains(string(encoded), "__aaaendpointserver") {
							t.Error("controller binding was copied into deployment parameters")
						}
					}
					if mode == "rejected" {
						w.WriteHeader(400)
						fmt.Fprint(w, "secret-error")
						return
					}
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprintf(w, `{"response":{"progress":"Template Deployemnt Id: %s"}}`, deploymentFixtureID)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(500)
					return
				}
				if duringRead != nil {
					callback := duringRead
					duringRead = nil
					callback()
				}
				var response any
				switch r.URL.Path {
				case devicesPath:
					response = []Device{{ID: "device", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}}
				case devicesPath + "/device":
					version := "17.18.04"
					if late && mode == "late-platform" {
						version = "26.02.01"
					}
					response = templateDevicePlatform{ID: "device", Serial: "FOC12345678", Family: "Switches and Hubs", SoftwareType: "IOS-XE", SoftwareVersion: version, Reachability: "Reachable"}
				case sitesPath:
					response = sampleSites()
				case assignedDevicesPath:
					response = []map[string]string{{"deviceId": "device", "siteId": "building"}}
				case wiredProvisionPath:
					response = []wiredProvisionedDevice{}
					if provisioned {
						response = []wiredProvisionedDevice{{ID: "provisioned", DeviceID: "device", SiteID: "building"}}
					}
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
					return
				case templatesPath:
					fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"Baseline"}]`)
					return
				case templateDeployStatusPath + deploymentFixtureID:
					status := "SUCCESS"
					if mode == "recover-pending" {
						status = "IN_PROGRESS"
					}
					if mode == "recover-failed" {
						status = "FAILURE"
					}
					json.NewEncoder(w).Encode(templateDeploymentStatus{ID: deploymentFixtureID, Project: "Lab", Name: "Baseline", Version: "2", Status: status, Start: submitted.Format(time.RFC3339), End: now.Format(time.RFC3339), Devices: []templateDeploymentDeviceStatus{{DeviceID: "device", Status: "SUCCESS", Start: submitted.Format(time.RFC3339), End: now.Format(time.RFC3339)}}})
					return
				case templateVersionsPath + "/base":
					versions++
					id, version := "committed", 2
					if late && mode == "late-version" || posts > 0 && mode == "recover-new-latest" {
						id = "new-version"
						version = 3
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": "base", "versionsInfo": []any{map[string]any{"id": id, "version": version}}}})
					return
				case templatesPath + "/committed", templatesPath + "/new-version":
					id, version, content := "committed", 2, "secret-content"
					if late && mode == "late-version" {
						id = "new-version"
						version = 3
					}
					if late && mode == "late-content" {
						content = "changed-secret-content"
					}
					parameter := map[string]any{"parameterName": "value", "dataType": "STRING", "required": true}
					if emptySelection {
						choices := map[string]any{}
						if late && mode == "late-empty-selection" {
							choices["new"] = "secret-choice"
						}
						parameter["required"] = false
						parameter["selection"] = map[string]any{"selectionType": "SINGLE_SELECT", "selectionValues": choices}
					}
					if binding {
						filter := "RADIUS"
						if late && mode == "late-binding" {
							filter = "TACACS"
						}
						parameter["binding"] = templateCommonSettingsBinding(t, filter)
						parameter["selection"] = map[string]any{"selectionType": "SINGLE_SELECT", "selectionValues": map[string]any{}}
					}
					json.NewEncoder(w).Encode(map[string]any{"id": id, "projectId": "project", "name": "Baseline", "version": version, "composite": false, "softwareType": "IOS-XE", "deviceTypes": []any{map[string]any{"productFamily": "Switches and Hubs"}}, "templateContent": content, "templateParams": []any{parameter}})
					return
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			target := regularTemplateTarget{template: nacmodel.TemplateIdentity{Project: "Lab", Name: "Baseline"}, redeploy: "NEVER", copyingConfig: true, forcePush: true, parameters: map[string][]string{"value": {"secret-original"}}}
			if omittedParameters {
				target.parameters = map[string][]string{}
			}
			write, err := c.planRegularTemplateDeployment(context.Background(), inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}, target)
			if err != nil || write == nil {
				t.Fatal("planning failed", err)
			}
			if posts != 0 || !strings.HasPrefix(write.resource, "template-deploy:base/committed/2/") || strings.Contains(write.resource, "secret") {
				t.Fatal("invalid planning evidence")
			}
			if write.identity.target != "device" || write.identity.parent != "building" || write.identity.serial == "" || write.action != "Update" {
				t.Fatal("missing physical identity")
			}
			if mode == "caller-change" {
				target.parameters["value"][0] = "secret-changed"
			}
			late = true
			receipt, err := write.submit(context.Background())
			success := mode == "accepted" || mode == "accepted-empty-selection" || mode == "accepted-binding" || mode == "caller-change" || strings.HasPrefix(mode, "recover-")
			if (err == nil) != success {
				t.Fatal("unexpected submission result", mode, err)
			}
			expectedPosts := 1
			if strings.HasPrefix(mode, "late-") {
				expectedPosts = 0
			}
			if posts != expectedPosts {
				t.Fatal("unexpected submission count", posts)
			}
			if success && receipt.ID != deploymentFixtureID || !success && receipt.ID != "" {
				t.Fatal("incorrect receipt")
			}
			if strings.HasPrefix(mode, "recover-") {
				beforeVersions := versions
				op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, Phase: "Accepted", Section: "inventory", TargetID: write.identity.target, TargetSerial: write.identity.serial, ParentID: write.identity.parent, TaskID: receipt.ID, SubmittedAt: &submitted}
				for _, phase := range []string{"Accepted", "Verifying"} {
					op.Phase = phase
					done, err := c.observeRegularTemplateDeployment(context.Background(), inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}, target, op, now)
					expectedDone := mode == "recover-success" || mode == "recover-empty-selection" || mode == "recover-binding" || mode == "recover-new-latest"
					if done != expectedDone || (err == nil) != (mode != "recover-failed") {
						t.Fatal("recovery result", mode, err)
					}
				}
				if posts != 1 || versions != beforeVersions {
					t.Fatal("recovery resubmitted or selected latest version")
				}
				if mode == "recover-success" {
					testOwnedTemplateRecovery(t, c, write, target, now, submitted, func(callback func()) { duringRead = callback })
					// The cleared-journal suppression fixture performs one
					// fresh plan, which must resolve latest before suppression.
					if posts != 1 || versions != beforeVersions+1 {
						t.Fatal("unexpected repeat-plan effects")
					}
					testRegularTemplateExecutor(t, c, write, target, now, submitted)
					testTemplateInventoryCompiler(t, c, func(present bool) { provisioned = present })
					if posts != 1 {
						t.Fatal("executor recovery replayed deployment")
					}
				}
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("sensitive data leaked")
			}
		})
	}
}
