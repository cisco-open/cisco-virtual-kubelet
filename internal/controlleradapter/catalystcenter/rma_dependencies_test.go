// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func rmaBindingFixture() (configurationDeviceReference, deviceReplacementIntent, []Device, []deviceReplacementRecord) {
	return configurationDeviceReference{Name: "edge", Serial: "OLD-SERIAL"}, deviceReplacementIntent{action: rmaReplace, replacementSerial: "NEW-SERIAL"}, []Device{{ID: "old-id", Hostname: "edge", Serial: "OLD-SERIAL", Reachability: "Unreachable"}, {ID: "new-id", Hostname: "spare", Serial: "NEW-SERIAL"}}, []deviceReplacementRecord{{ID: "record", FaultyID: "old-id", FaultySerial: "old-serial", Status: "READY-FOR-REPLACEMENT"}}
}

func TestRMABinding(t *testing.T) {
	for _, mode := range []string{"bound", "unmarked", "replacement-absent", "record-serial-absent", "duplicate-old", "duplicate-new", "wrong-old-serial", "wrong-new-serial", "other-workflow-old", "other-workflow-new", "other-faulty-new", "duplicate-record", "missing-declared-old", "self"} {
		t.Run(mode, func(t *testing.T) {
			ref, want, devices, records := rmaBindingFixture()
			switch mode {
			case "unmarked":
				records = nil
			case "replacement-absent":
				devices = devices[:1]
			case "record-serial-absent":
				records[0].FaultySerial = ""
			case "duplicate-old":
				devices = append(devices, Device{ID: "alias", Serial: "old-serial"})
			case "duplicate-new":
				devices = append(devices, Device{ID: "alias", Serial: "new-serial"})
			case "wrong-old-serial":
				records[0].FaultySerial = "other"
			case "wrong-new-serial":
				records[0].ReplacementSerial = "other"
			case "other-workflow-old":
				records = append(records, deviceReplacementRecord{ID: "other", FaultyID: "other-id", ReplacementSerial: "old-serial"})
			case "other-workflow-new":
				records = append(records, deviceReplacementRecord{ID: "other", FaultyID: "other-id", ReplacementSerial: "new-serial"})
			case "other-faulty-new":
				records = append(records, deviceReplacementRecord{ID: "other", FaultyID: "other-id", FaultySerial: "new-serial"})
			case "duplicate-record":
				records = append(records, records[0])
			case "missing-declared-old":
				ref.Serial = ""
			case "self":
				want.replacementSerial = "old-serial"
			}
			snapshot, err := bindDeviceReplacement(ref, want, devices, records)
			valid := mode == "bound" || mode == "unmarked" || mode == "replacement-absent" || mode == "record-serial-absent"
			if valid != (err == nil) {
				t.Fatal("unexpected binding", err)
			}
			if !valid {
				if snapshot != nil {
					t.Fatal("partial snapshot")
				}
				return
			}
			if snapshot.serial != "old-serial" || snapshot.target.Serial != "OLD-SERIAL" || snapshot.replacementSerial != "new-serial" {
				t.Fatal("lost serial identity or wire spelling")
			}
			if (snapshot.record == nil) != (mode == "unmarked") || (snapshot.replacement == nil) != (mode == "replacement-absent") {
				t.Fatal("invented evidence")
			}
			if snapshot.record != nil {
				records[0].WorkflowID = "changed"
				if snapshot.record.WorkflowID != "" {
					t.Fatal("aliased record")
				}
			}
			if snapshot.replacement != nil {
				devices[1].Serial = "changed"
				if snapshot.replacement.Serial != "NEW-SERIAL" {
					t.Fatal("aliased device")
				}
			}
		})
	}
}

func TestRMASnapshotRevalidation(t *testing.T) {
	for _, change := range []string{"none", "old-id", "old-serial", "new-id", "new-appears", "new-disappears", "workflow", "status", "record-disappears"} {
		t.Run(change, func(t *testing.T) {
			ref, want, devices, records := rmaBindingFixture()
			if change == "new-appears" {
				devices = devices[:1]
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Fatal("observation mutated")
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					rows = records
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			ctx := context.Background()
			before, err := c.deviceReplacementSnapshot(ctx, ref, want)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "old-id":
				devices[0].ID = "replaced-id"
			case "old-serial":
				devices[0].Serial = "replaced-serial"
			case "new-id":
				devices[1].ID = "replaced-id"
			case "new-appears":
				devices = append(devices, Device{ID: "new-id", Serial: "NEW-SERIAL"})
			case "new-disappears":
				devices = devices[:1]
			case "workflow":
				records[0].WorkflowID = "new-workflow"
			case "status":
				records[0].Status = "REPLACEMENT-IN-PROGRESS"
			case "record-disappears":
				records = []deviceReplacementRecord{}
			}
			err = c.revalidateDeviceReplacement(ctx, ref, want, before)
			if (err == nil) != (change == "none") {
				t.Fatal("changed evidence not fenced", err)
			}
		})
	}
}
