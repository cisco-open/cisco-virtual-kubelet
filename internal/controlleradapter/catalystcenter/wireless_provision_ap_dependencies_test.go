// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestWirelessProvisionSubmissionRechecksDependencies(t *testing.T) {
	for _, scenario := range []string{"unchanged", "changed dependencies", "missing receipt", "changed source"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			e := &wirelessProvisionExecutor{adapter: a, config: config}
			resources := []string(nil)
			if scenario == "missing receipt" {
				resources = []string{"locations/managed-ap:" + strings.Repeat("a", 64)}
				e.dependencyResources = resources
			}
			submissions := 0
			guarded := e.guardAPDependencies(&configurationWrite{submit: func(context.Context) (Task, error) { submissions++; return Task{ID: "task"}, nil }}, resources)
			if scenario == "changed dependencies" {
				e.dependencyResources = []string{"locations/managed-ap:" + strings.Repeat("b", 64)}
			}
			if scenario == "changed source" {
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			_, err := guarded.submit(ctx)
			if scenario == "unchanged" {
				if err != nil || submissions != 1 {
					t.Fatal("unchanged dependency submission rejected", err)
				}
			} else if err == nil || submissions != 0 {
				t.Fatal("invalid dependency reached native submission", err)
			}
		})
	}
}

func TestExistingAPPlacementReservationPacing(t *testing.T) {
	for _, scenario := range []string{"settled", "too recent", "future", "missing timestamp", "cancelled", "released", "static dependency"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", "provision-baseline", "Update", configurationResourceIdentity{target: "wlc", serial: "serial"}); err != nil {
				t.Fatal(err)
			}
			row, _ := settledConfigurationFixture(t, a)
			now := time.Now().UTC().Truncate(time.Second)
			row.Status.ManagerSettledAt = nil
			row.Status.Grant.Phase = "Held"
			row.CreationTimestamp = metav1.NewTime(now.Add(-11 * time.Second))
			resource := "locations/managed-ap:" + strings.Repeat("a", 64)
			e := &wirelessProvisionExecutor{adapter: a, config: config, apDependencies: []wirelessProvisionAPDependency{{prefix: "locations/", executor: &wirelessManagedAPExecutor{}}}}
			switch scenario {
			case "too recent":
				row.CreationTimestamp = metav1.NewTime(now.Add(-10 * time.Second))
			case "future":
				row.CreationTimestamp = metav1.NewTime(now.Add(time.Second))
			case "missing timestamp":
				row.CreationTimestamp = metav1.Time{}
			case "cancelled":
				row.Status.Grant = nil
				row.Status.PreparationCancelledAt = &metav1.Time{Time: now}
			case "released":
				row.Status.Grant.Phase = "Released"
			case "static dependency":
				e.dependencyResources = []string{resource}
			}
			desiredStatus := row.DeepCopy().Status
			if err := a.statusWriter.Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			row.Status = desiredStatus
			if err := a.statusWriter.Status().Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = e.dependenciesSettled(ctx, nc, []string{resource}, false, now)
			if (err == nil) != (scenario == "settled") {
				t.Fatal("unexpected existing-placement pacing", err)
			}
			if len(nc.Status.Configuration.ActionApplications) != 0 {
				t.Fatal("fabricated receipt for preexisting assignment")
			}
		})
	}
}
