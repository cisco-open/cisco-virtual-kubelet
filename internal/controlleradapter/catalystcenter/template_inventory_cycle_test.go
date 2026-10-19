// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	adaptercontract "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	"os"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

// Stateful manifest/compiler/native/journal qualification. Worker/manager
// reservation evidence is supplied by fixtures; this is not a real Lease test.
func TestTemplateInventoryManifestCycle(t *testing.T) {
	for _, mode := range []string{"empty-selection", "composite-empty-selection", "pacing-wall-clock", "composite-two-devices-pacing-wall-clock", "private-onchange-rollback", "composite-private-onchange-rollback", "private-onchange", "composite-private-onchange", "provision", "reprovision", "lost-receipt", "failed-task", "configmap", "configmap-before-submit", "configmap-after-submit", "configmap-replaced", "configmap-deleted", "multiple-templates", "second-template-failed", "second-template-lost", "composite", "composite-lost", "composite-failed", "mixed-composite", "composite-new-member", "composite-configmap", "composite-configmap-before-submit", "composite-configmap-after-submit", "composite-configmap-replaced", "composite-configmap-deleted", "composite-two-devices", "composite-two-devices-wrong-status"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			sourceMode := strings.TrimPrefix(mode, "composite-")
			rollback := strings.HasSuffix(sourceMode, "-rollback")
			a, config := journalFixture(t)
			raw := strings.Replace(wiredInventoryModel(), `"state":"PROVISION"`, `"state":"PROVISION","dayn_templates":{"regular":[{"name":"Lab#Baseline"}]}`, 1)
			if mode == "reprovision" {
				raw = strings.Replace(raw, `"PROVISION"`, `"REPROVISION"`, 1)
			}
			multiple := mode == "multiple-templates" || strings.HasPrefix(mode, "second-template-")
			steps := []string{"assign", "provision", "template"}
			if multiple {
				raw = strings.Replace(raw, `{"name":"Lab#Baseline"}`, `{"name":"Lab#Baseline"},{"name":"Lab#Telemetry"}`, 1)
				steps = append(steps, "template-second")
			}
			composite := strings.HasPrefix(mode, "composite") || mode == "mixed-composite"
			if composite {
				if mode == "mixed-composite" {
					raw = strings.Replace(raw, `"regular":[{"name":"Lab#Baseline"}]`, `"regular":[{"name":"Lab#Baseline"}],"composite":[{"name":"Lab#Combined"}]`, 1)
					steps = append(steps, "template-composite")
				} else {
					raw = strings.Replace(raw, `"regular":[{"name":"Lab#Baseline"}]`, `"composite":[{"name":"Lab#Combined"}]`, 1)
				}
			}
			multiDevice := strings.HasPrefix(mode, "composite-two-devices")
			if multiDevice {
				var root map[string]any
				if err := json.Unmarshal([]byte(raw), &root); err != nil {
					t.Fatal(err)
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				devices := inventory["devices"].([]any)
				other := map[string]any{}
				for key, value := range devices[0].(map[string]any) {
					other[key] = value
				}
				other["name"], other["serial_number"] = "other", "FOC87654321"
				inventory["devices"] = append(devices, other)
				encoded, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(encoded)
				steps = []string{"template", "template-other"}
			}
			if strings.HasPrefix(sourceMode, "private-onchange") {
				raw = strings.Replace(raw, `"dayn_templates":{`, `"dayn_templates":{"redeploy_template":"ON_CHANGE",`, 1)
			}
			if rollback {
				reference := `"name":"Lab#Baseline"`
				variable := `,"variables":[{"name":"value","value":"private-a"}]`
				if composite {
					reference = `"name":"Lab#Combined"`
					variable = `,"variables":[{"template_name":"Baseline","name":"value","value":"private-a"}]`
				}
				raw = strings.Replace(raw, reference, reference+variable, 1)
				steps = append(steps, "template", "template")
			}
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(raw)
			if strings.HasPrefix(sourceMode, "private-onchange") {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: "comparison", Name: "template-comparison", Key: "key"}}
				if err := a.statusWriter.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "inventory", Path: templateComparisonScope, Source: "comparison", Use: "PrivateComparisonKey"}}
				a.intentSecretPath = t.TempDir()
				relative, err := adaptercontract.IntentSecretRelativePath(adaptercontract.IntentSecretPathInput{ConfigName: config.Name, Section: "inventory", JSONPointer: templateComparisonScope, SourceAlias: "comparison", SecretName: "template-comparison", SecretKey: "key"})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(a.intentSecretPath, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.Repeat("ab", 32)), 0600); err != nil {
					t.Fatal(err)
				}
			}

			var model *core.ConfigMap
			if strings.HasPrefix(sourceMode, "configmap") {
				model = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "template-model", UID: "template-model-uid"}, Data: map[string]string{"model.yaml": raw}}
				if err := a.statusWriter.Create(ctx, model); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: model.Name, Key: "model.yaml"}
			}
			changeSource := func() {
				t.Helper()
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
					t.Fatal(err)
				}
				reference := "Lab#Baseline"
				if composite {
					reference = "Lab#Combined"
				}
				model.Data["model.yaml"] = strings.Replace(raw, reference, "Lab#Replacement", 1)
				if err := a.statusWriter.Update(ctx, model); err != nil {
					t.Fatal(err)
				}
			}

			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			assigned := multiDevice
			provisioned := mode == "reprovision" || multiDevice
			writes := []string{}
			submitted := time.Now()
			taskID := ""
			const secondDeploymentID = "22222222-2222-4222-8222-222222222222"
			deploymentParameters := []string{}
			expectedParameter := "private-a"
			deploymentName := "Baseline"
			deploymentDevice := "edge-id"
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if strings.HasPrefix(r.URL.Path, taskPath) {
					id := strings.TrimPrefix(r.URL.Path, taskPath)
					if id == deploymentFixtureID || id == secondDeploymentID {
						t.Error("deployment receipt sent to generic task API")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "isError": false, "errorCode": "FAILED", "startTime": submitted.UnixMilli(), "endTime": time.Now().UnixMilli()}})
					return
				}
				if r.URL.Path == assignDevicesPath && r.Method == "POST" {
					assigned = true
					writes = append(writes, "assign")
					taskID = "assign-task"
					submitted = time.Now()
					fmt.Fprint(w, `{"response":{"taskId":"assign-task"}}`)
					return
				}
				if r.URL.Path == wiredProvisionPath && r.Method != "GET" {
					if !assigned {
						t.Error("provisioning before assignment")
					}
					method := "POST"
					if mode == "reprovision" {
						method = "PUT"
					}
					if r.Method != method {
						t.Error("wrong provision method", r.Method)
					}
					provisioned = true
					writes = append(writes, "provision")
					taskID = "provision-task"
					submitted = time.Now()

					fmt.Fprint(w, `{"response":{"taskId":"provision-task"}}`)
					return
				}
				if r.URL.Path == templateDeployPath && r.Method == "POST" {
					if !assigned || !provisioned {
						t.Error("deployment before provisioning")
					}
					var request struct {
						TemplateID string           `json:"templateId"`
						MainID     string           `json:"mainTemplateId"`
						Composite  bool             `json:"isComposite"`
						Members    []map[string]any `json:"memberTemplateDeploymentInfo"`
						Targets    []struct {
							ID      string         `json:"id"`
							Version string         `json:"versionedTemplateId"`
							Params  map[string]any `json:"params"`
						} `json:"targetInfo"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Targets) != 1 || (request.Targets[0].ID != "edge-id" && !(multiDevice && request.Targets[0].ID == "other-id")) {
						t.Error("invalid deployment target payload")
						w.WriteHeader(400)
						return
					}
					deploymentDevice = request.Targets[0].ID
					step, version := "template", "committed"
					taskID, deploymentName = deploymentFixtureID, "Baseline"
					if request.TemplateID == "combined-committed" && composite {
						deploymentName = "Combined"
						version = ""
						if mode == "mixed-composite" {
							step = "template-composite"
							taskID = secondDeploymentID
						}
						if !request.Composite || request.MainID != "combined" || len(request.Members) != 2 || request.Members[0]["templateId"] != "committed" || request.Members[1]["templateId"] != "second-committed" {
							t.Error("wrong composite member payload")
						}
					} else if request.TemplateID == "second" && multiple {
						step, version = "template-second", "second-committed"
						taskID, deploymentName = secondDeploymentID, "Telemetry"
						if strings.Join(writes, ",") != "assign,provision,template" {
							t.Error("second template executed out of order", writes)
						}
					} else if request.TemplateID != "base" {
						t.Error("unexpected template", request.TemplateID)
					}
					if multiDevice && deploymentDevice == "other-id" {
						step, taskID = "template-other", secondDeploymentID
						if strings.Join(writes, ",") != "template" {
							t.Error("second device deployed out of order", writes)
						}
					}
					if request.Targets[0].Version != version {
						t.Error("wrong committed version")
					}
					if sourceMode == "empty-selection" {
						params := request.Targets[0].Params
						if composite {
							targets, ok := request.Members[0]["targetInfo"].([]any)
							if !ok || len(targets) != 1 {
								t.Error("missing optional member target")
								w.WriteHeader(400)
								return
							}
							params = targets[0].(map[string]any)["params"].(map[string]any)
						}
						if len(params) != 0 {
							t.Error("omitted optional selection was synthesized into payload")
						}
					}
					if rollback {
						params := request.Targets[0].Params
						if composite {
							targets, ok := request.Members[0]["targetInfo"].([]any)
							if !ok || len(targets) != 1 {
								t.Error("missing member target")
								w.WriteHeader(http.StatusBadRequest)
								return
							}
							params = targets[0].(map[string]any)["params"].(map[string]any)
						}
						value, ok := params["value"].(string)
						if !ok || value != expectedParameter {
							t.Error("wrong private target parameter")
						}
						deploymentParameters = append(deploymentParameters, value)
						taskID = fmt.Sprintf("%08d-1111-4111-8111-111111111111", len(deploymentParameters))
					}
					writes = append(writes, step)
					submitted = time.Now()
					if mode == "lost-receipt" || mode == "composite-lost" || (mode == "second-template-lost" && step == "template-second") {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprintf(w, `{"response":{"progress":"Template Deployemnt Id: %s"}}`, taskID)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					devices := []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}}
					if multiDevice {
						devices = append(devices, Device{ID: "other-id", Hostname: "other", Serial: "FOC87654321", Family: "Switches and Hubs", Reachability: "Reachable"})
					}
					rows = devices
				case devicesPath + "/other-id":
					rows = templateDevicePlatform{ID: "other-id", Serial: "FOC87654321", Family: "Switches and Hubs", SoftwareType: "IOS-XE", SoftwareVersion: "17.18.04", Reachability: "Reachable"}
				case devicesPath + "/edge-id":
					rows = templateDevicePlatform{ID: "edge-id", Serial: "FOC12345678", Family: "Switches and Hubs", SoftwareType: "IOS-XE", SoftwareVersion: "17.18.04", Reachability: "Reachable"}
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
					return
				case templatesPath:
					if composite {
						fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"Baseline"},{"templateId":"second","projectId":"project","name":"Telemetry"},{"templateId":"combined","projectId":"project","name":"Combined"}]`)
					} else if multiple {
						fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"Baseline"},{"templateId":"second","projectId":"project","name":"Telemetry"}]`)
					} else {
						fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"Baseline"}]`)
					}
					return
				case templateVersionsPath + "/combined":
					fmt.Fprint(w, `[{"templateId":"combined","versionsInfo":[{"id":"combined-committed","version":2}]}]`)
					return
				case templatesPath + "/combined-committed":
					fmt.Fprint(w, `{"id":"combined-committed","projectId":"project","name":"Combined","version":2,"composite":true,"softwareType":"IOS-XE","deviceTypes":[{"productFamily":"Switches and Hubs"}],"containingTemplates":[{"id":"base","name":"Baseline"},{"id":"second","name":"Telemetry"}]}`)
					return
				case templatesPath + "/new-committed":
					fmt.Fprint(w, `{"id":"new-committed","projectId":"project","name":"Baseline","version":3,"composite":false,"softwareType":"IOS-XE","deviceTypes":[{"productFamily":"Switches and Hubs"}],"templateParams":[]}`)
					return
				case templateVersionsPath + "/base":
					if mode == "composite-new-member" && len(writes) >= 3 {
						fmt.Fprint(w, `[{"templateId":"base","versionsInfo":[{"id":"new-committed","version":3}]}]`)
						return
					}
					fmt.Fprint(w, `[{"templateId":"base","versionsInfo":[{"id":"committed","version":2}]}]`)
					return
				case templatesPath + "/committed":
					if sourceMode == "empty-selection" {
						fmt.Fprint(w, `{"id":"committed","projectId":"project","name":"Baseline","version":2,"composite":false,"softwareType":"IOS-XE","deviceTypes":[{"productFamily":"Switches and Hubs"}],"templateParams":[{"parameterName":"optional","dataType":"STRING","required":false,"defaultValue":"","selection":{"selectionType":"SINGLE_SELECT","selectionValues":{}}}]}`)
						return
					}
					if rollback {
						fmt.Fprint(w, `{"id":"committed","projectId":"project","name":"Baseline","version":2,"composite":false,"softwareType":"IOS-XE","deviceTypes":[{"productFamily":"Switches and Hubs"}],"templateParams":[{"parameterName":"value","dataType":"STRING","required":true}]}`)
						return
					}
					fmt.Fprint(w, `{"id":"committed","projectId":"project","name":"Baseline","version":2,"composite":false,"softwareType":"IOS-XE","deviceTypes":[{"productFamily":"Switches and Hubs"}],"templateParams":[]}`)
					return
				case templateVersionsPath + "/second":
					fmt.Fprint(w, `[{"templateId":"second","versionsInfo":[{"id":"second-committed","version":2}]}]`)
					return
				case templatesPath + "/second-committed":
					fmt.Fprint(w, `{"id":"second-committed","projectId":"project","name":"Telemetry","version":2,"composite":false,"softwareType":"IOS-XE","deviceTypes":[{"productFamily":"Switches and Hubs"}],"templateParams":[]}`)
					return
				case templateDeployStatusPath + deploymentFixtureID, templateDeployStatusPath + secondDeploymentID, templateDeployStatusPath + "00000001-1111-4111-8111-111111111111", templateDeployStatusPath + "00000002-1111-4111-8111-111111111111", templateDeployStatusPath + "00000003-1111-4111-8111-111111111111":
					if r.URL.Path != templateDeployStatusPath+taskID {
						t.Error("recovery selected wrong deployment receipt")
					}
					status := "SUCCESS"
					if mode == "failed-task" || mode == "composite-failed" || (mode == "second-template-failed" && taskID == secondDeploymentID) {
						status = "FAILURE"
					}
					end := time.Now().Format(time.RFC3339Nano)
					start := submitted.Format(time.RFC3339Nano)
					observedDevice := deploymentDevice
					if mode == "composite-two-devices-wrong-status" && taskID == secondDeploymentID {
						observedDevice = "edge-id"
					}
					json.NewEncoder(w).Encode(templateDeploymentStatus{ID: taskID, Project: "Lab", Name: deploymentName, Version: "2", Status: status, Start: start, End: end, Devices: []templateDeploymentDeviceStatus{{DeviceID: observedDevice, Status: "SUCCESS", Start: start, End: end}}})
					return
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					values := []map[string]string{}
					if assigned {
						values = append(values, map[string]string{"deviceId": "edge-id", "siteId": "building"})
						if multiDevice {
							values = append(values, map[string]string{"deviceId": "other-id", "siteId": "building"})
						}
					}
					rows = values
				case wiredProvisionPath:
					values := []wiredProvisionedDevice{}
					if provisioned {
						if multiDevice && r.URL.Query().Get("networkDeviceId") == "other-id" {
							values = append(values, wiredProvisionedDevice{ID: "other-provisioned", SiteID: "building", DeviceID: "other-id"})
						} else {
							values = append(values, wiredProvisionedDevice{ID: "provisioned", SiteID: "building", DeviceID: "edge-id"})
						}
					}
					rows = values
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			evaluate := func() string {
				t.Helper()
				restarted := *a
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, handled, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, &configapi.NetworkControllerConfigStatus{})
				if !handled && !(sourceMode == "configmap-deleted" && reason == "SourceUnavailable") {
					t.Fatal("public manifest router rejected intent", reason)
				}
				return reason
			}
			for stepIndex, step := range steps {
				if rollback && stepIndex >= 3 {
					old, next := "private-a", "private-b"
					if stepIndex == 4 {
						old, next = next, old
					}
					expectedParameter = next
					config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), old, next, 1))
					config.Generation++
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}

				reason := evaluate()
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Prepared" {
					t.Fatal("no prepared operation", reason, nc.Status.Configuration)
				}
				op := nc.Status.Configuration.Operation.DeepCopy()
				if composite && strings.HasPrefix(op.Resource, "template-composite-deploy:") {
					if op.DependencyVersions == nil || len(op.DependencyVersions.Entries) != 3 {
						t.Fatal("prepared graph missing")
					}
					if _, err := contract.OperationDigest(op); err != nil {
						t.Fatal("prepared composite cannot bind reservation", err)
					}
				}

				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				var plan configurationApplyPlan
				if strings.HasPrefix(sourceMode, "private-onchange") {
					plan, err = a.compilePrivateTemplateInventoryApply(nc, config, source)
				} else {
					plan, err = a.compileAdmittedInventoryApply(config, source)
				}
				if err != nil {
					t.Fatal(err)
				}
				write, err := plan.executor.next(ctx)
				if isTemplateDeploymentResource(op.Resource) && errors.Is(err, errConfigurationBusy) {
					before := len(writes)
					for range 2 {
						// Fresh compiler/worker observations must preserve the wait.
						evaluate()
					}
					held := journalState(t, a).Operation
					if held == nil || held.ID != op.ID || held.Phase != "Prepared" || len(writes) != before {
						t.Fatal("settling replayed a write or replaced preparation")
					}
					if strings.HasSuffix(mode, "pacing-wall-clock") {
						deadline := time.Now().Add(15 * time.Second)
						for errors.Is(err, errConfigurationBusy) && time.Now().Before(deadline) {
							time.Sleep(time.Second)
							evaluate()
							held := journalState(t, a).Operation
							if held == nil || held.ID != op.ID || len(writes) != before {
								t.Fatal("wall-clock settling lost journal or submitted early")
							}
							write, err = plan.executor.next(ctx)
						}
						if err != nil {
							t.Fatal("real-time wait did not settle", err)
						}
					} else {
						// Simulate elapsed wall time in persisted evidence. This is a
						// fake API/actor test, not an installed clock/Lease qualification.
						nc, err = a.currentController(ctx)
						if err != nil {
							t.Fatal(err)
						}
						nc.Status.Configuration.Operation.PreparedAt = &metav1.Time{Time: time.Now().Add(-20 * time.Second)}
						for i := range nc.Status.Configuration.ActionApplications {
							receipt := &nc.Status.Configuration.ActionApplications[i]
							if validWiredProvisionResource(receipt.Resource) {
								receipt.Operation.SubmittedAt = &metav1.Time{Time: time.Now().Add(-time.Minute)}
								receipt.VerifiedAt = &metav1.Time{Time: time.Now().Add(-20 * time.Second)}
							}
						}
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						op = nc.Status.Configuration.Operation.DeepCopy()
						write, err = plan.executor.next(ctx)
					}
				}
				if err != nil || write == nil || write.resource != op.Resource {
					t.Fatal("prepared restart mismatch", write, err)
				}
				row, _ := settledConfigurationFixture(t, a)
				row.Status.ManagerSettledAt = nil
				row.Status.Grant.Phase = "Held"
				row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: row.Status.Grant.GrantedAt}
				if err := a.statusWriter.Status().Update(ctx, row); err != nil {
					t.Fatal(err)
				}
				if step == "template" && (sourceMode == "configmap-before-submit" || sourceMode == "configmap-replaced" || sourceMode == "configmap-deleted") {
					if sourceMode == "configmap-before-submit" {
						changeSource()
					} else {
						if err := a.statusWriter.Delete(ctx, model); err != nil {
							t.Fatal(err)
						}
						if sourceMode == "configmap-replaced" {
							model.ResourceVersion = ""
							model.UID = types.UID("replacement-model-uid")
							if err := a.statusWriter.Create(ctx, model); err != nil {
								t.Fatal(err)
							}
						}
					}
					if _, err := write.submit(ctx); err == nil {
						t.Fatal("stale ConfigMap permitted submission")
					}
					evaluate()
					if len(writes) != 2 || journalState(t, a).Operation == nil {
						t.Fatal("stale operation lost or submitted", writes)
					}
					return
				}
				// The common dispatch implementation is used with its test-only permit;
				// native calls are never made by the planner or recovery observation.
				err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "inventory", write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
					task, err := write.submit(ctx)
					return configurationReceipt{task: task}, err
				}, journalTestAdmission, write.identity)
				if (step == "template" && (mode == "lost-receipt" || mode == "composite-lost")) || (step == "template-second" && mode == "second-template-lost") {
					if err == nil || journalState(t, a).Operation.Phase != "Unknown" {
						t.Fatal("ambiguous receipt not retained", err)
					}
					evaluate()
					if len(writes) != len(steps) {
						t.Fatal("ambiguous submission replayed", writes)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if step == "template" && sourceMode == "configmap-after-submit" {
					changeSource()
					for range 3 {
						evaluate()
					}
					retained := journalState(t, a).Operation
					if len(writes) != 3 || retained == nil || retained.ID != op.ID || retained.Phase != "Accepted" || retained.SourceUID != op.SourceUID || retained.SourceVersion != op.SourceVersion {
						t.Fatal("changed source replayed or retired accepted deployment", writes, retained)
					}
					return
				}
				if reason := evaluate(); (step == "template" && (mode == "failed-task" || mode == "composite-failed")) || (step == "template-second" && mode == "second-template-failed") {
					if reason != "ControllerTaskFailed" || journalState(t, a).Operation.Phase != "Accepted" {
						t.Fatal(reason)
					}
					return
				}
				if mode == "composite-two-devices-wrong-status" && step == "template-other" {
					for range 3 {
						evaluate()
					}
					retained := journalState(t, a).Operation
					if len(writes) != 2 || retained == nil || retained.ID != op.ID || retained.TargetID != "other-id" || retained.Phase != "Accepted" {
						t.Fatal("other-device status settled or replayed accepted deployment", writes, retained)
					}
					return
				}
				if journalState(t, a).Operation.Phase != "Verifying" {
					t.Fatal("task did not reach verification", taskID)
				}
				if rollback {
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) && journalState(t, a).Operation.Phase == "Verifying" {
						evaluate()
						if journalState(t, a).Operation.Phase == "Verifying" {
							time.Sleep(20 * time.Millisecond)
						}
					}
				}
				if reason := evaluate(); journalState(t, a).Operation.Phase != "Verified" {
					t.Fatal("manifest verification failed", reason)
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), row); err != nil {
					t.Fatal(err)
				}
				row.Status.Completion, err = contract.CompletionForReceipt(row, nc, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				row.Status.Grant.Phase = "Released"
				row.Status.ManagerSettledAt = &metav1.Time{Time: time.Now()}
				if err := a.statusWriter.Status().Update(ctx, row); err != nil {
					t.Fatal(err)
				}
				evaluate()
				if journalState(t, a).Operation != nil {
					t.Fatal("settled journal retained")
				}
				if rollback {
					// Simulate manager retirement only after independent completion and journal release.
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), row); err != nil {
						t.Fatal(err)
					}
					if row.Status.Completion == nil || row.Status.ManagerSettledAt == nil || row.Status.Grant.Phase != "Released" {
						t.Fatal("reservation not ready for simulated retirement")
					}
					row.Finalizers = nil
					if err := a.statusWriter.Update(ctx, row); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Delete(ctx, row); err != nil {
						t.Fatal(err)
					}
				}
			}
			if strings.Join(writes, ",") != strings.Join(steps, ",") {
				t.Fatal(writes)
			}
			for range 3 {
				evaluate()
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("public status did not converge", config.Status)
			}
			expectedReceipts := len(steps)
			if rollback {
				expectedReceipts-- // rollback replaces the earlier A fingerprint receipt.
				if strings.Join(deploymentParameters, ",") != "private-a,private-b,private-a" {
					t.Fatal("parameter change/rollback was skipped")
				}
			}
			if len(writes) != len(steps) || journalState(t, a).Operation != nil || len(journalState(t, a).ActionApplications) != expectedReceipts {
				t.Fatal("revision replay or lost receipts", writes)
			}
			if strings.HasPrefix(sourceMode, "private-onchange") {
				// A distinct source generation with identical target inputs is
				// comparable after restart, despite formatting changes.
				var document any
				if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &document); err != nil {
					t.Fatal(err)
				}
				formatted, err := json.MarshalIndent(document, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = formatted
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					evaluate()
				}
				if len(writes) != len(steps) || journalState(t, a).Operation != nil {
					t.Fatal("unchanged target inputs replayed across generations", writes)
				}
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("unchanged private inputs did not converge across generations", config.Status)
				}

			}

		})
	}
}
