//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Structural API qualification only: native proof values are fixtures, not a
// claim that a controller created, retired or recreated a real lab object.
func TestEnvtestSiteRecreationHistory(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, latest := range []bool{false, true} {
		for _, mode := range []string{"roundtrip", "drop-history", "drop-successor", "edit-history"} {
			t.Run(fmt.Sprintf("latest-%t/%s", latest, mode), func(t *testing.T) {
				ctx := context.Background()
				a, config := journalFixture(t)
				installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("site-history-%t-%s", latest, mode))(a, config)
				state, op, row := siteRecreationFixture(t, latest)
				complete := publishSiteRecreationFixture(t, state, op, row)
				appendSiteRetirementFixture(t, complete, 4, "5", "retire-task-2", time.Now().Add(-20*time.Millisecond))
				second, next, newRow := stageSiteRecreationFixture(t, complete, 5, "6", "recreate-task-2", "33333333-3333-4333-8333-333333333333", time.Now().Add(-10*time.Millisecond))
				complete = publishSiteRecreationFixture(t, second, next, newRow)
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration = complete
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				stored, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.persistSiteCreationPublication(ctx, stored); err != nil {
					t.Fatal("complete cohort and successor not retained together", err)
				}
				stored, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				cohorts, err := siteHistoryCohorts(stored.Status.Configuration)
				if err != nil || len(cohorts) != 2 {
					t.Fatal("archive roundtrip lost rooted history", err)
				}
				if mode == "roundtrip" {
					return
				}
				rows := stored.Status.Configuration.ActionApplications
				switch mode {
				case "drop-history":
					stored.Status.Configuration.ActionApplications = rows[1:]
				case "drop-successor":
					stored.Status.Configuration.ActionApplications = rows[:len(rows)-1]
				case "edit-history":
					rows[0].Operation.TaskID = "changed-original"
				}
				if err := a.statusWriter.Status().Update(ctx, stored); err != nil {
					t.Fatal(err)
				}
				after, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := siteCreatedRecords(after.Status.Configuration); err == nil {
					t.Fatal("serialized history corruption passed creator reader")
				}
				if _, err := siteAppliedRecords(after.Status.Configuration); err == nil {
					t.Fatal("serialized history corruption passed applied reader")
				}
				if _, err := siteRetiredRecords(after.Status.Configuration); err == nil {
					t.Fatal("serialized history corruption passed retirement reader")
				}
			})
		}
	}
}

func TestEnvtestSiteCreationPublicationCAS(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range siteCreationPublicationModes {
		t.Run(mode, func(t *testing.T) {
			exerciseSiteCreationPublicationCAS(t, mode, installWirelessFeatureQualificationAPI(t, api, "creation-publish-"+mode))
		})
	}
}
