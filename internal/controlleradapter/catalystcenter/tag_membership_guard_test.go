// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func TestTagMembershipPlansRevalidateWholeSelection(t *testing.T) {
	for _, memberType := range []string{"template", "networkdevice"} {
		for _, mode := range []string{"valid", "tag-replaced", "member-replaced", "protection-flip", "during-reobserve-protection", "later-tag-replaced", "later-member-replaced", "became-present", "frozen", "unrelated-member", "missing-receipt", "read-error", "project-replaced"} {
			t.Run(memberType+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				changed, writes, tagReads, lateProtected := false, 0, 0, false
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method == "POST" {
						writes++
						var body map[string][]string
						if r.URL.Path != tagsPath+"/selected/member" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || len(body[memberType]) != 1 || body[memberType][0] != "first-id" {
							t.Error("frozen target/type/tag changed")
						}
						if mode == "missing-receipt" {
							fmt.Fprint(w, `{}`)
						} else {
							fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						}
						return
					}
					if changed && mode == "read-error" {
						w.WriteHeader(400)
						return
					}
					project := "project"
					if changed && mode == "project-replaced" {
						project = "replacement-project"
					}
					switch r.URL.Path {
					case tagsPath:
						tagReads++
						rows := []map[string]any{}
						for _, name := range []string{"selected", "later"} {
							id, protected := name, false
							if changed && (name == "selected" && mode == "tag-replaced" || name == "later" && mode == "later-tag-replaced") {
								id += "-replacement"
							}
							if changed && name == "later" && (mode == "protection-flip" || mode == "during-reobserve-protection" && lateProtected) {
								protected = true
							}
							rows = append(rows, map[string]any{"id": id, "name": name, "systemTag": protected})
						}
						json.NewEncoder(w).Encode(map[string]any{"response": rows})
					case devicesPath, templatesPath:
						rows := []map[string]any{}
						for _, name := range []string{"first", "second"} {
							id := name + "-id"
							if changed && (name == "first" && mode == "member-replaced" || name == "second" && mode == "later-member-replaced") {
								id += "-replacement"
							}
							if memberType == "template" {
								rows = append(rows, map[string]any{"templateId": id, "name": name, "projectId": project})
							} else {
								rows = append(rows, map[string]any{"id": id, "hostname": name, "serialNumber": name})
							}
						}
						if memberType == "template" {
							json.NewEncoder(w).Encode(rows)
						} else {
							json.NewEncoder(w).Encode(map[string]any{"response": rows})
						}
					case templateProjectsPath:
						json.NewEncoder(w).Encode([]map[string]any{{"id": project, "name": "Project"}})
					default:
						if mode == "during-reobserve-protection" && tagReads > 1 {
							lateProtected = true
						}
						rows := []map[string]string{}
						if changed && mode == "became-present" {
							rows = append(rows, map[string]string{"instanceUuid": "first-id"})
						}
						if changed && mode == "unrelated-member" {
							rows = append(rows, map[string]string{"instanceUuid": "foreign"})
						}
						json.NewEncoder(w).Encode(map[string]any{"response": rows})
					}
				})
				device := &deviceMembershipExecutor{client: c, devices: []desiredDeviceMembership{{device: configurationDeviceReference{Name: "first", Serial: "first"}, tags: []string{"selected"}}, {device: configurationDeviceReference{Name: "second", Serial: "second"}, tags: []string{"later"}}}}
				template := &templateMembershipExecutor{client: c, intent: templateIntent{definitions: []desiredTemplate{{identity: nacmodel.TemplateIdentity{Project: "Project", Name: "first"}, tags: []string{"selected"}}, {identity: nacmodel.TemplateIdentity{Project: "Project", Name: "second"}, tags: []string{"later"}}}}}
				var write *configurationWrite
				var err error
				if memberType == "template" {
					write, err = template.next(ctx)
				} else {
					write, err = device.next(ctx)
				}
				if err != nil || write == nil {
					t.Fatal("missing initial membership plan", err)
				}
				changed = true
				if mode == "frozen" {
					device.devices[0].device.Name = "caller-mutated"
					device.devices[0].tags[0] = "mutated-tag"
					template.intent.definitions[0].identity.Name = "caller-mutated"
					template.intent.definitions[0].tags[0] = "mutated-tag"
				}
				_, err = write.submit(ctx)
				valid := mode == "valid" || mode == "frozen" || mode == "unrelated-member" || memberType == "networkdevice" && mode == "project-replaced"
				if (err == nil) != valid {
					t.Fatal("unexpected membership guard outcome", err)
				}
				expected := 0
				if valid || mode == "missing-receipt" {
					expected = 1
				}
				if writes != expected {
					t.Fatal("stale selection wrote a membership")
				}
				if _, err := write.submit(ctx); err == nil || writes != expected {
					t.Fatal("consumed membership closure replayed")
				}
			})
		}
	}
}
