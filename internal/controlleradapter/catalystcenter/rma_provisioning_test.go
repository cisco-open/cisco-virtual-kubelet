// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAMarkProvisioningBinding(t *testing.T) {
	for _, mode := range []string{"bound", "missing-site", "unassigned", "missing-provision", "wrong-provision-site", "late-site", "late-provision", "recovery-site", "recovery-provision"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			ref, want, devices, _ := rmaBindingFixture()
			want.site = "Global/Lab/Building"
			late, marked := false, false
			posts := 0
			submitted := time.Now().UTC().Truncate(time.Second).Add(-time.Second)
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == deviceReplacementPath {
					posts++
					marked = true
					fmt.Fprint(w, `{"response":{"taskId":"mark-task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(500)
					return
				}
				siteID := "building"
				if late && (mode == "late-site" || mode == "recovery-site") {
					siteID = "new-building"
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					records := []deviceReplacementRecord{}
					if marked {
						records = append(records, deviceReplacementRecord{ID: "mark-record", FaultyID: "old-id", FaultySerial: "OLD-SERIAL", Status: "MARKED-FOR-REPLACEMENT", CreatedAt: submitted.UnixMilli()})
					}
					rows = records
				case sitesPath:
					sites := sampleSites()
					for i := range sites {
						if sites[i].ID == "building" {
							sites[i].ID = siteID
							if mode == "missing-site" {
								sites[i].NameHierarchy = "Global/Other/Building"
							}
						}
					}
					rows = sites
				case assignedDevicesPath:
					members := []map[string]string{}
					if mode != "unassigned" {
						members = append(members, map[string]string{"deviceId": "old-id", "siteId": siteID})
					}
					rows = members
				case wiredProvisionPath:
					if r.URL.Query().Get("networkDeviceId") != "old-id" {
						t.Error("unscoped provision read")
					}
					id := "provision-record"
					if late && (mode == "late-provision" || mode == "recovery-provision") {
						id = "new-provision-record"
					}
					if mode == "wrong-provision-site" {
						siteID = "other-site"
					}
					records := []wiredProvisionedDevice{}
					if mode != "missing-provision" {
						records = append(records, wiredProvisionedDevice{ID: id, SiteID: siteID, DeviceID: "old-id"})
					}
					rows = records
				case taskPath + "mark-task":
					rows = map[string]any{"id": "mark-task", "isError": false, "startTime": submitted.UnixMilli(), "endTime": time.Now().UnixMilli()}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			write, err := c.planDeviceReplacementMark(ctx, ref, want)
			initiallyInvalid := mode == "missing-site" || mode == "unassigned" || mode == "missing-provision" || mode == "wrong-provision-site"
			if initiallyInvalid {
				if err == nil || write != nil || posts != 0 {
					t.Fatal("invalid retained provisioning accepted", err)
				}
				return
			}
			if err != nil || write == nil || write.identity.parent != "building" {
				t.Fatal("site not bound", err)
			}
			if mode == "late-site" || mode == "late-provision" {
				late = true
			}
			task, err := write.submit(ctx)
			if late {
				if err == nil || posts != 0 {
					t.Fatal("changed prerequisite submitted")
				}
				return
			}
			if err != nil || posts != 1 {
				t.Fatal("mark submission failed", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Accepted", Section: "inventory", Action: "Create", Resource: write.resource, TargetID: write.identity.target, TargetSerial: write.identity.serial, ParentID: write.identity.parent, TaskID: task.ID, SubmittedAt: &metav1.Time{Time: submitted}}
			if mode == "recovery-site" || mode == "recovery-provision" {
				late = true
			}
			evidence, err := c.observeDeviceReplacementMark(ctx, ref, want, op, time.Now())
			if (err == nil) != (mode == "bound") || (evidence != nil) != (mode == "bound") || posts != 1 {
				t.Fatal("recovery lost prerequisite binding", err)
			}
		})
	}
}

func TestRMAMarkResourceScope(t *testing.T) {
	ref, want, devices, _ := rmaBindingFixture()
	snapshot, err := bindDeviceReplacement(ref, want, devices, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.siteID, snapshot.provisionID = "site", "provision"
	original, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.replacementSerial = "another-spare"
	snapshot.replacement = nil
	changed, err := deviceReplacementMarkResource(snapshot)
	if err != nil || original != changed {
		t.Fatal("spare changes repeated old-device mark")
	}
	snapshot.provisionID = "new-provision"
	changed, err = deviceReplacementMarkResource(snapshot)
	if err != nil || original == changed {
		t.Fatal("provision incarnation not bound")
	}
	snapshot.provisionID = "provision"
	snapshot.siteID = "new-site"
	changed, err = deviceReplacementMarkResource(snapshot)
	if err != nil || original == changed {
		t.Fatal("site incarnation not bound")
	}
}
