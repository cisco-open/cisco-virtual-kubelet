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

func TestCompositeDeploymentContent(t *testing.T) {
	for _, mode := range []string{"stable", "pinned", "reordered", "new-member-version", "changed-body", "missing-members", "duplicate-member", "recursive", "nested-composite", "wrong-member-name", "wrong-project", "missing-member", "wrong-pinned-order", "wrong-pinned-count", "wrong-pinned-parent", "bad-member-version", "declared-members", "wrong-declared-order", "wrong-declared-count", "physical-stable", "physical-unassigned", "physical-unprovisioned", "physical-unreachable", "physical-late-serial", "physical-late-provision", "physical-late-platform", "physical-late-member", "execution-stable", "execution-lost", "execution-late-member", "execution-newest", "execution-wrong-graph", "execution-pending", "execution-failed"} {
		t.Run(mode, func(t *testing.T) {
			late := false
			latestReads := 0
			var duringRead func()
			forceUnprovisioned := false
			posts := 0
			submitted := time.Now()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" && duringRead != nil {
					hook := duringRead
					duringRead = nil
					hook()
				}
				if strings.HasPrefix(mode, "execution-") && r.URL.Path == templateDeployPath && r.Method == "POST" {
					posts++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["templateId"] != "composite-committed" || body["mainTemplateId"] != "composite" || body["isComposite"] != true {
						t.Error("wrong composite native request")
					}
					if mode == "execution-lost" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprintf(w, `{"response":{"progress":"Template Deployemnt Id: %s"}}`, deploymentFixtureID)
					}
					return
				}
				if r.URL.Path == templateDeployStatusPath+deploymentFixtureID {
					status := "SUCCESS"
					if mode == "execution-pending" {
						status = "IN_PROGRESS"
					}
					if mode == "execution-failed" {
						status = "FAILURE"
					}
					start, end := submitted.Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano)
					json.NewEncoder(w).Encode(templateDeploymentStatus{ID: deploymentFixtureID, Project: "Lab", Name: "Combined", Version: "2", Status: status, Start: start, End: end, Devices: []templateDeploymentDeviceStatus{{DeviceID: "edge-id", Status: "SUCCESS", Start: start, End: end}}})
					return
				}
				if r.Method != "GET" {
					t.Error("content reader attempted mutation")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case devicesPath:
					device := Device{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}
					if mode == "physical-unreachable" {
						device.Reachability = "Unreachable"
					}
					if late && mode == "physical-late-serial" {
						device.Serial = "FOC99999999"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{device}})
					return
				case devicesPath + "/edge-id":
					platform := templateDevicePlatform{ID: "edge-id", Serial: "FOC12345678", Family: "Switches and Hubs", SoftwareType: "IOS-XE", SoftwareVersion: "17.18.04", Reachability: "Reachable"}
					if late && mode == "physical-late-platform" {
						platform.SoftwareVersion = "26.02.01"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": platform})
					return
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				case assignedDevicesPath:
					rows := []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
					if mode == "physical-unassigned" {
						rows = []map[string]string{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				case wiredProvisionPath:
					id := "provisioned"
					if late && mode == "physical-late-provision" {
						id = "replacement"
					}
					rows := []wiredProvisionedDevice{{ID: id, DeviceID: "edge-id", SiteID: "building"}}
					if mode == "physical-unprovisioned" || forceUnprovisioned {
						rows = []wiredProvisionedDevice{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"},{"id":"other-project","name":"Shared"}]`)
					return
				case templatesPath:
					rows := []templateSummary{{ID: "composite", ProjectID: "project", Name: "Combined"}, {ID: "baseline", ProjectID: "project", Name: "Baseline"}, {ID: "telemetry", ProjectID: "other-project", Name: "Telemetry"}}
					if mode == "missing-member" {
						rows = rows[:2]
					}
					json.NewEncoder(w).Encode(rows)
					return
				}
				if strings.HasPrefix(r.URL.Path, templateVersionsPath+"/") {
					latestReads++
					id := strings.TrimPrefix(r.URL.Path, templateVersionsPath+"/")
					versionID := id + "-committed"
					number := 2
					if late && (mode == "new-member-version" || mode == "pinned" || mode == "physical-late-member" || mode == "execution-late-member" || mode == "execution-newest") && id == "telemetry" {
						versionID = "telemetry-new"
						number = 3
					}
					fmt.Fprintf(w, `[{"templateId":%q,"versionsInfo":[{"id":%q,"version":%d}]}]`, id, versionID, number)
					return
				}
				id := strings.TrimPrefix(r.URL.Path, templatesPath+"/")
				name, project, composite := "", "project", false
				switch id {
				case "composite-committed":
					name, composite = "Combined", true
				case "baseline-committed":
					name = "Baseline"
				case "telemetry-committed", "telemetry-new":
					name, project = "Telemetry", "other-project"
				default:
					t.Error("unexpected content read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				number := 2
				if id == "telemetry-new" {
					number = 3
				}
				if mode == "bad-member-version" && id == "baseline-committed" {
					number = 9
				}
				if mode == "nested-composite" && id == "baseline-committed" {
					composite = true
				}
				body := map[string]any{"id": id, "name": name, "projectId": project, "version": number, "composite": composite, "templateContent": "sensitive-content", "softwareType": "IOS-XE", "deviceTypes": []any{map[string]any{"productFamily": "Switches and Hubs"}}}
				if late && mode == "changed-body" && id == "baseline-committed" {
					body["templateContent"] = "changed-sensitive-content"
				}
				if id == "composite-committed" {
					rows := []any{map[string]any{"id": "baseline", "name": "Baseline", "projectName": "Lab"}, map[string]any{"id": "telemetry", "name": "Telemetry", "projectName": "Shared"}}
					switch mode {
					case "missing-members":
						rows = nil
					case "duplicate-member":
						rows[1] = rows[0]
					case "recursive":
						rows[0] = map[string]any{"id": "composite"}
					case "wrong-member-name":
						rows[0].(map[string]any)["name"] = "Wrong"
					case "wrong-project":
						rows[1].(map[string]any)["projectName"] = "Lab"
					case "reordered":
						if late {
							rows[0], rows[1] = rows[1], rows[0]
						}
					}
					body["containingTemplates"] = rows
				}
				json.NewEncoder(w).Encode(body)
			})
			ctx := context.Background()
			identity := nacmodel.TemplateIdentity{Project: "Lab", Name: "Combined", Composite: true}
			if strings.Contains(mode, "declared") {
				identity.Members = []string{"Lab#Baseline", "Shared#Telemetry"}
				if mode == "wrong-declared-order" {
					identity.Members[0], identity.Members[1] = identity.Members[1], identity.Members[0]
				}
				if mode == "wrong-declared-count" {
					identity.Members = identity.Members[:1]
				}
			}
			if strings.HasPrefix(mode, "execution-") {
				device := inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge", Serial: "FOC12345678"}, site: "Global/Lab/Building"}
				target := compositeTemplateTarget{template: identity, redeploy: "NEVER", copyingConfig: true, forcePush: true}
				write, err := c.planCompositeTemplateDeployment(ctx, device, target)
				if err != nil || write == nil || posts != 0 {
					t.Fatal("composite planning failed or mutated", err)
				}
				if mode == "execution-late-member" {
					late = true
				}
				task, err := write.submit(ctx)
				if mode == "execution-late-member" {
					if err == nil || posts != 0 {
						t.Fatal("stale member submitted")
					}
					return
				}
				if mode == "execution-lost" {
					if err == nil || posts != 1 {
						t.Fatal("missing receipt accepted")
					}
					return
				}
				if err != nil || posts != 1 || task.ID != deploymentFixtureID {
					t.Fatal("native submission failed", err)
				}
				op := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: write.resource, Action: "Update", Phase: "Accepted", TargetID: write.identity.target, ParentID: write.identity.parent, TargetSerial: write.identity.serial, DependencyVersions: write.identity.dependencies.DeepCopy(), TaskID: task.ID, SubmittedAt: &metav1.Time{Time: submitted}}
				if mode == "execution-wrong-graph" {
					op.DependencyVersions.Entries[1].Version++
				}
				reads := latestReads
				late = true
				for _, phase := range []string{"Accepted", "Verifying"} {
					op.Phase = phase
					done, err := c.observeCompositeTemplateDeployment(ctx, device, target, op, time.Now())
					switch mode {
					case "execution-pending":
						if done || err != nil {
							t.Fatal("pending deployment completed", err)
						}
					case "execution-failed", "execution-wrong-graph":
						if done || err == nil {
							t.Fatal("failed or rebound graph accepted")
						}
					default:
						if !done || err != nil {
							t.Fatal("pinned recovery failed", err)
						}
					}
				}
				if mode == "execution-stable" {
					testOwnedCompositeRecovery(t, c, device, target, write, submitted, func(hook func()) { duringRead = hook })
					reads += 3 // One fresh parent/two-member plan after simulated settlement.
					testCompositeExecutor(t, c, device, target, write, submitted)
					reads += 6 // Exact Prepared and changed-graph replans; no submitted-phase planning.
				}
				if posts != 1 || latestReads != reads {
					t.Fatal("observation replayed or selected latest", posts, latestReads, reads)
				}
				if mode == "execution-stable" {
					testCompositeInventoryCompiler(t, c, func(value bool) { forceUnprovisioned = value })
					if posts != 1 {
						t.Fatal("compiler performed native mutation")
					}
				}

				return
			}
			if strings.HasPrefix(mode, "physical-") {
				device := inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge", Serial: "FOC12345678"}, site: "Global/Lab/Building"}
				snapshot, err := c.compositeTemplateDependenciesAtVersion(ctx, device, identity, nil)
				valid := mode == "physical-stable" || strings.HasPrefix(mode, "physical-late-")
				if !valid {
					if err == nil || snapshot != nil {
						t.Fatal("physical prerequisites bypassed")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if payload, err := snapshot.payload(true, true, nil); err != nil || len(payload) == 0 {
					t.Fatal("bound composite payload failed", err)
				}
				late = true
				err = c.revalidateCompositeTemplateDependencies(ctx, device, identity, snapshot)
				if (mode == "physical-stable") != (err == nil) {
					t.Fatal("physical revalidation failed", err)
				}
				return
			}
			var pinned *compositeDeploymentVersionBinding
			if strings.Contains(mode, "pinned") {
				pinned = &compositeDeploymentVersionBinding{parent: templateDeploymentVersionBinding{baseID: "composite", version: templateVersion{ID: "composite-committed", Number: 2}}, members: []templateDeploymentVersionBinding{{baseID: "baseline", version: templateVersion{ID: "baseline-committed", Number: 2}}, {baseID: "telemetry", version: templateVersion{ID: "telemetry-committed", Number: 2}}}}
				switch mode {
				case "wrong-pinned-order":
					pinned.members[0], pinned.members[1] = pinned.members[1], pinned.members[0]
				case "wrong-pinned-count":
					pinned.members = pinned.members[:1]
				case "wrong-pinned-parent":
					pinned.parent.baseID = "other"
				}
			}
			content, err := c.compositeTemplateContent(ctx, identity, pinned)
			valid := mode == "declared-members" || mode == "stable" || mode == "pinned" || mode == "reordered" || mode == "new-member-version" || mode == "changed-body"
			if !valid {
				if err == nil || content != nil {
					t.Fatal("invalid dependency graph accepted")
				}
				if strings.Contains(err.Error(), "sensitive-content") {
					t.Fatal("content leaked")
				}
				return
			}
			if err != nil || content == nil || len(content.members) != 2 || content.members[0].base.ID != "baseline" || content.members[1].base.ID != "telemetry" {
				t.Fatal("ordered content resolution failed", err)
			}
			late = true
			if mode == "pinned" {
				recovered, err := c.compositeTemplateContent(ctx, identity, pinned)
				if err != nil || recovered.members[1].version.ID != "telemetry-committed" || latestReads != 0 {
					t.Fatal("recovery substituted latest version", err, latestReads)
				}
			} else {
				err := c.revalidateCompositeTemplateContent(ctx, identity, content)
				if (mode == "stable" || mode == "declared-members") != (err == nil) {
					t.Fatal("dependency revalidation result", err)
				}
			}
		})
	}
}

func TestCompositeContentPayloadValidation(t *testing.T) {
	for _, mode := range []string{"valid", "unknown-member", "required", "wrong-platform", "duplicate", "nil-member"} {
		t.Run(mode, func(t *testing.T) {
			fixture := compositeDeploymentFixture()
			body := func() map[string]any {
				return map[string]any{"softwareType": "IOS-XE", "deviceTypes": []any{map[string]any{"productFamily": "Switches and Hubs"}}, "templateParams": []any{}}
			}
			content := &compositeDeploymentContent{parent: &regularTemplateContent{base: fixture.base, version: fixture.version, body: body()}}
			for _, member := range fixture.members {
				content.members = append(content.members, &regularTemplateContent{base: member.base, version: member.version, body: body()})
			}
			platform := templateDevicePlatform{ID: "device", SoftwareType: "IOS-XE", Family: "Switches and Hubs"}
			parameters := map[string]map[string][]string{}
			switch mode {
			case "unknown-member":
				parameters["Missing"] = map[string][]string{"password": {"secret"}}
			case "required":
				content.members[0].body["templateParams"] = []any{map[string]any{"parameterName": "password", "dataType": "STRING", "required": true}}
			case "wrong-platform":
				content.members[1].body["softwareType"] = "IOS-XR"
			case "duplicate":
				content.members = append(content.members, content.members[0])
			case "nil-member":
				content.members[0] = nil
			}
			raw, err := content.payload(platform, true, true, parameters)
			if mode == "valid" {
				if err != nil || len(raw) == 0 {
					t.Fatal(err)
				}
			} else if err == nil || raw != nil {
				t.Fatal("invalid member inputs accepted")
			}
		})
	}
}
