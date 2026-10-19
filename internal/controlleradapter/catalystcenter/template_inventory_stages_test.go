// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestTemplateInventoryPrerequisites(t *testing.T) {
	ctx := context.Background()
	a, _ := journalFixture(t)
	for _, sync := range []bool{false, true} {
		calls := 0
		valid := true
		write := &configurationWrite{resource: "prerequisite"}
		if sync {
			write.submitCompleted = func(context.Context) error { calls++; return nil }
		} else {
			write.submit = func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }
		}
		prior := &wirelessStageFixture{write: write}
		s := &templateInventoryStages{adapter: a, prerequisites: configurationApplyPlan{section: "inventory", executor: prior}, templates: []templateStageBinding{{executor: &regularTemplateExecutor{}, baseID: "base", deviceID: "device", serial: "serial"}}, validate: func(context.Context) error {
			if !valid {
				return errConfigurationStale
			}
			return nil
		}}
		planned, err := s.next(ctx)
		if err != nil || planned == nil || planned.resource != "prerequisite" {
			t.Fatal("dependent action bypassed prerequisite", err)
		}
		valid = false
		if sync {
			err = planned.submitCompleted(ctx)
		} else {
			_, err = planned.submit(ctx)
		}
		if err == nil || calls != 0 {
			t.Fatal("late invalidation allowed submit")
		}
		valid = true
		prior.write = nil
		if planned, err := s.next(ctx); err == nil || planned != nil {
			t.Fatal("invalid deployment executor silently completed")
		}
		if s.count() != 2 {
			t.Fatal("wrong resource count")
		}
		op := &cvk.NetworkControllerConfigurationOperation{Phase: "Accepted", Section: "inventory", Resource: "prerequisite", TaskID: "task", SubmittedAt: &metav1.Time{Time: time.Now().Add(-time.Minute)}}
		observed := 0
		s.prerequisites.observeCompletion = func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
			observed++
			return true, nil
		}
		if done, err := s.observeCompletion(ctx, op, time.Now()); err != nil || !done || observed != 1 {
			t.Fatal("prerequisite completion was not routed", err)
		}
		if done, err := s.verify(ctx, op); err != nil || !done || prior.verifies != 1 {
			t.Fatal("prerequisite verification was not routed", err)
		}
	}
}

func TestTemplateStageSelectsKindAndPhysicalIdentity(t *testing.T) {
	regular, composite := &regularTemplateExecutor{}, &compositeTemplateExecutor{}
	for _, mode := range []string{"composite", "regular", "serial", "device", "duplicate", "missing-graph", "wrong-kind"} {
		t.Run(mode, func(t *testing.T) {
			stage := &templateInventoryStages{templates: []templateStageBinding{{executor: regular, baseID: "parent", deviceID: "device", serial: "serial"}, {executor: composite, baseID: "parent", deviceID: "device", serial: "serial"}}}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: "template-composite-deploy:" + strings.Repeat("a", 64), TargetID: "device", TargetSerial: "serial", DependencyVersions: dependencyJournalIdentity().dependencies}
			switch mode {
			case "regular":
				op.Resource = "template-deploy:parent/parent-v2/2/" + strings.Repeat("a", 64)
				op.DependencyVersions = nil
			case "serial":
				op.TargetSerial = "replacement"
			case "device":
				op.TargetID = "replacement"
			case "duplicate":
				stage.templates = append(stage.templates, stage.templates[1])
			case "missing-graph":
				op.DependencyVersions = nil
			case "wrong-kind":
				stage.templates = stage.templates[:1]
			}
			selected, err := stage.selected(op)
			if mode == "composite" {
				if err != nil || selected != composite {
					t.Fatal("wrong composite route", err)
				}
			} else if mode == "regular" {
				if err != nil || selected != regular {
					t.Fatal("wrong regular route", err)
				}
			} else if err == nil || selected != nil {
				t.Fatal("ambiguous/stale route accepted")
			}
		})
	}
}
