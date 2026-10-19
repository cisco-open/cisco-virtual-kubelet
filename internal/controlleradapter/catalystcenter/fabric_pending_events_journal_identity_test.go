// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFabricPendingEventJournalReceipt(t *testing.T) {
	for _, mode := range []string{"accepted", "verifying", "unknown", "prepared", "verified", "missing-task", "missing-group", "changed-group", "foreign-fabric", "foreign-event", "wrong-source", "wrong-site", "replaced-node", "missing-member", "changed-serial", "future-time"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			event := fabricPendingEvent{ID: "event-id", FabricID: "fabric-id"}
			targets := []contract.GroupTargetIdentity{{InventoryID: "device-id", PhysicalIdentity: "serial-a", Device: ops.ConfigurationReservationReference{Name: "a", UID: "device-a"}, Node: ops.ConfigurationReservationReference{Name: "a", UID: "node-a"}}}
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config-uid", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Resource: pendingFabricEventResource(want.path, "building", event), Action: "Update", Phase: "Prepared", TargetID: event.ID, ParentID: event.FabricID}
			var err error
			op, err = contract.WithGroupIdentity(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			op.Phase, op.TaskID = "Accepted", "task-id"
			op.SubmittedAt = &metav1.Time{Time: now}
			// Use the actual JSON representation so restart tests include schema names.
			raw, _ := json.Marshal(op)
			var restored cvk.NetworkControllerConfigurationOperation
			if json.Unmarshal(raw, &restored) != nil {
				t.Fatal("journal round trip")
			}
			op = &restored
			pubsub := true
			snapshot := &fabricPendingEventsSnapshot{site: fabricSiteState{want: want, siteID: "building", exists: true, actual: fabricSite{ID: "fabric-id", SiteID: "building", Authentication: "Closed Authentication", PubSub: &pubsub}}, devices: []Device{{ID: "device-id", Serial: "serial-a"}}}
			switch mode {
			case "verifying":
				op.Phase = "Verifying"
			case "unknown":
				op.Phase = "Unknown"
			case "prepared":
				op.Phase = "Prepared"
			case "verified":
				op.Phase = "Verified"
			case "missing-task":
				op.TaskID = ""
			case "missing-group":
				op.PhysicalGroup = nil
			case "changed-group":
				op.PhysicalGroup.Targets[0].Node.UID = "replacement"
			case "foreign-fabric":
				op.ParentID = "foreign"
			case "foreign-event":
				op.TargetID = "foreign"
			case "wrong-source":
				op.SourceVersion = "changed"
			case "wrong-site":
				snapshot.site.siteID = "replacement-site"
			case "replaced-node":
				targets[0].Node.UID = "replacement"
			case "missing-member":
				snapshot.devices = nil
			case "changed-serial":
				snapshot.devices[0].Serial = "replacement"
			case "future-time":
				op.SubmittedAt.Time = now.Add(time.Hour)
			}
			got, err := pendingFabricEventJournalReceipt(want, op, snapshot, targets, now)
			valid := mode == "accepted" || mode == "verifying" || mode == "unknown"
			if (err == nil) != valid {
				t.Fatal("journal binding", err)
			}
			if valid && (got.event != event || got.taskID != "task-id" || !got.submitted.Equal(now)) {
				t.Fatal("lost recovery identity")
			}
		})
	}
}
