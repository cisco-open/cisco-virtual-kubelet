// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"testing"
)

func TestResumePreparedPhysicalReceiptKeepsIdentityAndRejectsReplay(t *testing.T) {
	for _, scenario := range []string{"accepted", "uncertain", "different resource", "different target", "different source", "changed journal", "missing admission", "accepted snapshot"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			identity := configurationResourceIdentity{target: "device", serial: "SERIAL"}
			op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			resource := "device"
			var source *configSourceSnapshot
			admissions, submissions := 0, 0
			var admit configurationAdmissionStep = func(ctx context.Context) (configurationDispatchCheck, error) {
				admissions++
				return journalTestAdmission(ctx)
			}
			switch scenario {
			case "different resource":
				resource = "other"
			case "different target":
				identity.target = "other"
			case "different source":
				source = &configSourceSnapshot{version: "changed"}
			case "changed journal":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation.Resource = "changed"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "missing admission":
				admit = nil
			case "accepted snapshot":
				op.Phase = "Accepted"
			}
			submit := func(context.Context) (configurationReceipt, error) {
				submissions++
				if scenario == "uncertain" {
					return configurationReceipt{}, errors.New("response lost")
				}
				return configurationReceipt{task: Task{ID: "task"}}, nil
			}
			err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "sites", resource, "Update", submit, admit, identity)
			stored := journalState(t, a).Operation
			if stored == nil || stored.ID != op.ID {
				t.Fatal("resume replaced operation identity")
			}
			if scenario == "accepted" || scenario == "uncertain" {
				phase := "Accepted"
				if scenario == "uncertain" {
					phase = "Unknown"
				}
				if admissions != 1 || submissions != 1 || stored.Phase != phase || ((err == nil) != (scenario == "accepted")) {
					t.Fatal("resume outcome", err, stored.Phase, admissions, submissions)
				}
				if err := a.submitPreparedConfigurationReceipt(ctx, config, source, op, "sites", resource, "Update", submit, admit, identity); err == nil {
					t.Fatal("old Prepared snapshot replayed")
				}
				if admissions != 1 || submissions != 1 {
					t.Fatal("replay entered admission or remote API")
				}
			} else if err == nil || admissions != 0 || submissions != 0 || stored.Phase != "Prepared" {
				t.Fatal("changed operation reached admission", err, admissions, submissions)
			}
		})
	}
}
