// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"
)

func TestLiveCatalystCenterIntentComplianceEvidence(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// This is an inspection boundary, not a fabricated native task completion.
	// It lets this read-only test verify that old or absent live reports fail
	// the freshness predicate without authorizing any journal transition.
	inspectionStart := time.Now().UTC()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		device, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal(err)
		}
		evidence, err := c.readIntentCompliance(ctx, device.ID)
		if err != nil {
			t.Fatal("typed compliance read unavailable", err)
		}
		observedAt := time.Now().UTC()
		for _, kind := range []string{"NETWORK_SETTINGS", "NETWORK_PROFILE"} {
			row, present := evidence[kind]
			freshnessErr := freshIntentCompliance(evidence, device.ID, []string{kind}, inspectionStart, observedAt)
			if (!present || row.checkedAt.IsZero() || row.checkedAt.Before(inspectionStart)) && freshnessErr == nil {
				t.Fatal("old or absent live report passed the inspection freshness boundary")
			}
			if !present {
				t.Logf("%s %s absent; cannot supply convergence evidence", ip, kind)
				continue
			}
			t.Logf("%s %s status=%s state=%s checked_at=%s", ip, kind, row.status, row.state, row.checkedAt.Format(time.RFC3339))
		}
	}
	t.Log("read-only compliance evidence inspected for three lab identities; no task completion/source correlation, remediation or release was inferred")
}
