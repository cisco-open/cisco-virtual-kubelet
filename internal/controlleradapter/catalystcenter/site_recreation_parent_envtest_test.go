//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"
)

// Synthetic native proofs qualify schema/CAS/history retention, not installed
// ownership adoption or a native parent replacement.
func TestEnvtestSiteRecreationParentHandoff(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "drop-parent-archive", "edit-parent-reference", "unknown-version"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			installWirelessFeatureQualificationAPI(t, api, "parent-handoff-"+mode)(a, config)
			state, _, row := siteParentHandoffFixture(t)
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = state
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation.DeepCopy()
			row.Operation = *op.DeepCopy()
			row.Operation.Phase = "Verified"
			row.VerifiedAt = op.SubmittedAt.DeepCopy()
			complete, err := siteCreationPublication(nc.Status.Configuration, op, row)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = complete
			if err := a.persistSiteCreationPublication(ctx, nc); err != nil {
				t.Fatal("parent handoff CAS did not preserve both chains", err)
			}
			stored, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			archives, err := siteHistoryCohorts(stored.Status.Configuration)
			if err != nil || len(archives) != 2 {
				t.Fatal("serialized handoff lost parent or child predecessor", err)
			}
			if mode == "complete" {
				return
			}
			rows := stored.Status.Configuration.ActionApplications
			switch mode {
			case "drop-parent-archive":
				stored.Status.Configuration.ActionApplications = rows[1:]
			case "edit-parent-reference":
				parts, _ := siteRecreationParts(rows[len(rows)-1].Operation.Resource)
				parts[6] = strings.Repeat("0", 64)
				rows[len(rows)-1].Operation.Resource = siteRecreateHandoffPrefix + strings.Join(parts, ":")
			case "unknown-version":
				rows[len(rows)-1].Operation.Resource = strings.Replace(rows[len(rows)-1].Operation.Resource, ":v2:", ":v3:", 1)
			}
			if err := a.statusWriter.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			stored, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := siteCreatedRecords(stored.Status.Configuration); err == nil {
				t.Fatal("corrupt serialized handoff accepted")
			}
			if err := a.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("corrupt handoff released completion fence")
			}
		})
	}
}
