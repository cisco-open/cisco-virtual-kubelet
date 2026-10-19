// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"testing"
	"time"
)

func TestWirelessProvisionApplicationPersistsBeforeRelease(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	before := wirelessProvisionBaseline{device: Device{ID: "wlc", Hostname: "WLC", Serial: "serial", ManagementIP: "198.51.100.10"}, siteID: "site", lastProvisioned: "100"}
	record, err := wirelessProvisionRecordFor("WLC", "Update", before)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := record.resource()
	if err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(ctx, config, nil, "inventory", resource, "Update", configurationResourceIdentity{target: "wlc", serial: "serial"})
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	submitted := metav1.NewTime(time.Now().Add(-time.Minute))
	nc.Status.Configuration.Operation.Phase = "Verifying"
	nc.Status.Configuration.Operation.SubmittedAt = &submitted
	nc.Status.Configuration.Operation.TaskID = "task"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	stamp := "100"
	changeSourceDuringRead := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case devicesPath:
			if changeSourceDuringRead {
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Error(err)
				}
				changeSourceDuringRead = false
			}
			d := before.device
			d.Family = "Wireless Controller"
			d.Reachability = "Reachable"
			json.NewEncoder(w).Encode(map[string]any{"response": []Device{d}})
		case taskPath + "task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task", "isError": false, "startTime": submitted.UnixMilli(), "endTime": time.Now().UnixMilli()}})
		case "/dna/intent/api/v1/wirelessControllers/wlc/provisionStatus":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"networkDeviceId": "wlc", "deviceName": "WLC", "siteId": "site", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "NOT_STARTED", "lastProvisionedTimeStamp": stamp, "outOfSync": false}})
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	if err := a.recordWirelessProvisionApplication(ctx, config, nil, op.ID, "WLC"); err == nil {
		t.Fatal("old success became receipt")
	}
	if len(journalState(t, a).ActionApplications) != 0 {
		t.Fatal("premature receipt")
	}
	stamp = "101"
	if err := a.recordWirelessProvisionApplication(ctx, config, nil, op.ID, "WLC"); err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || !configurationActionReceiptPresent(state, state.Operation) {
		t.Fatal("receipt not persisted")
	}
	copy := state.DeepCopy()
	copy.ActionApplications[0].Operation.SubmittedAt.Time = time.Time{}
	if state.ActionApplications[0].Operation.SubmittedAt.IsZero() {
		t.Fatal("aliased receipt")
	}
	candidate := state.Operation.DeepCopy()
	record.Timestamp = "101"
	candidate.Resource, err = record.resource()
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := wirelessProvisionAlreadyApplied(state, candidate); err != nil || !applied {
		t.Fatal("same revision replayed", err)
	}
	for _, field := range []string{"generation", "controller", "source"} {
		changed := candidate.DeepCopy()
		switch field {
		case "generation":
			changed.ConfigGeneration++
		case "controller":
			changed.ControllerGeneration++
		case "source":
			changed.SourceVersion = "new"
		}
		if applied, err := wirelessProvisionAlreadyApplied(state, changed); err != nil || applied {
			t.Fatal("changed revision inherited receipt", field, err)
		}
	}
	saved := state.ActionApplications[0]
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.ActionApplications = nil
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("pruned receipt released operation")
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, saved)
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	restarted := *a
	settledConfigurationFixture(t, &restarted)
	if err := restarted.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if state := journalState(t, &restarted); state.Operation != nil || len(state.ActionApplications) != 1 {
		t.Fatal("receipt lost on release")
	}
	ref := configurationDeviceReference{Name: "WLC", Serial: "serial"}
	if write, err := restarted.planWirelessProvisionRevision(ctx, config, nil, ref, "REPROVISION", "site", false); err != nil || write != nil {
		t.Fatal("same revision planned another action", err)
	}
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	next, err := restarted.planWirelessProvisionRevision(ctx, config, nil, ref, "REPROVISION", "site", false)
	if err != nil || next == nil {
		t.Fatal("new revision suppressed", err)
	}
	// Even a receipt-aware plan is not a device mutation grant.
	if err := restarted.submitConfigurationWrite(ctx, config, nil, "inventory", next); err != errConfigurationDeviceAdmission {
		t.Fatal("revision planning bypassed admission", err)
	}
	pending, err := restarted.prepareConfiguration(ctx, config, nil, "inventory", next.resource, next.action, next.identity)
	if err != nil {
		t.Fatal(err)
	}
	if write, err := restarted.planWirelessProvisionRevision(ctx, config, nil, ref, "REPROVISION", "site", false); err != errConfigurationBusy || write != nil {
		t.Fatal("pending operation ignored", err)
	}
	if journalState(t, &restarted).Operation.ID != pending.ID {
		t.Fatal("pending evidence changed")
	}

	settledConfigurationFixture(t, &restarted)
	if err := restarted.finishConfiguration(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	changeSourceDuringRead = true
	if write, err := restarted.planWirelessProvisionRevision(ctx, config, nil, ref, "REPROVISION", "site", false); err != errConfigurationStale || write != nil {
		t.Fatal("source edit during reads accepted", err)
	}
	if journalState(t, &restarted).Operation != nil {
		t.Fatal("stale planning wrote journal")
	}

}
