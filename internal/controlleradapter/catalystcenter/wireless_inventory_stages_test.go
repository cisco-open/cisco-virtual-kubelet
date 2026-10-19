// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"testing"
)

type wirelessStageFixture struct {
	write           *configurationWrite
	err             error
	calls, verifies int
}

func (e *wirelessStageFixture) count() int { return 1 }
func (e *wirelessStageFixture) next(context.Context) (*configurationWrite, error) {
	e.calls++
	return e.write, e.err
}
func (e *wirelessStageFixture) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	e.verifies++
	return true, e.err
}

func TestWirelessInventoryStageOrdering(t *testing.T) {
	ctx := context.Background()
	prerequisite := &wirelessStageFixture{write: &configurationWrite{resource: "assignments/wlc"}}
	provision := &wirelessStageFixture{err: errConfigurationBusy}
	checks := 0
	stages := &wirelessInventoryStages{prerequisites: prerequisite, provisioning: []configurationExecutor{provision}, validate: func(context.Context) error { checks++; return nil }}
	write, err := stages.next(ctx)
	if err != nil || write != prerequisite.write || provision.calls != 0 || checks != 1 {
		t.Fatal("prerequisite blocked by dependent planning", err)
	}
	prerequisite.write = nil
	if write, err := stages.next(ctx); write != nil || !errors.Is(err, errConfigurationBusy) || provision.calls != 1 {
		t.Fatal("provision wait lost", err)
	}
	provision.err = nil
	provision.write = &configurationWrite{resource: "durable-provision-baseline"}
	write, err = stages.next(ctx)
	if err != nil || write != provision.write || write.resource != "durable-provision-baseline" {
		t.Fatal("provision identity changed", err)
	}
	stages.validate = func(context.Context) error { return errConfigurationStale }
	before := prerequisite.calls
	if _, err := stages.next(ctx); !errors.Is(err, errConfigurationStale) || prerequisite.calls != before {
		t.Fatal("invalid prospective dependencies allowed planning")
	}
	stages.validate = nil
	if _, err := stages.next(ctx); err == nil {
		t.Fatal("missing preflight allowed planning")
	}
	// Recovery verifies the exact prerequisite despite changed future planning.
	done, err := stages.verify(ctx, &cvk.NetworkControllerConfigurationOperation{Resource: "assignments/wlc"})
	if err != nil || !done || prerequisite.verifies != 1 {
		t.Fatal("prerequisite receipt recovery lost", err)
	}
}

func TestWirelessInventoryStageRecoverySelectsExactName(t *testing.T) {
	record, err := wirelessProvisionRecordFor("wlc", "Update", wirelessProvisionBaseline{device: Device{ID: "wlc-id", Hostname: "wlc", Serial: "serial", ManagementIP: "198.51.100.10"}, siteID: "site", lastProvisioned: "100"})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := record.resource()
	if err != nil {
		t.Fatal(err)
	}
	op := &cvk.NetworkControllerConfigurationOperation{Resource: resource}
	executor := &wirelessProvisionExecutor{reference: configurationDeviceReference{Name: "wlc", Serial: "serial"}}
	stages := &wirelessInventoryStages{provisioning: []configurationExecutor{executor, executor}}
	if _, err := stages.verify(context.Background(), op); !errors.Is(err, errConfigurationOwned) {
		t.Fatal("duplicate executor accepted", err)
	}
	stages.provisioning = []configurationExecutor{&wirelessProvisionExecutor{reference: configurationDeviceReference{Name: "other"}}}
	if _, err := stages.verify(context.Background(), op); !errors.Is(err, errConfigurationStale) {
		t.Fatal("unmatched receipt accepted", err)
	}
}
