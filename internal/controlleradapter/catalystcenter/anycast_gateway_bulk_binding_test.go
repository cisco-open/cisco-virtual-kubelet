// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAnycastBulkBindingRequiresExactSourceOwnerAndJournal(t *testing.T) {
	for _, mode := range []string{"valid", "disabled", "invalid-flag", "missing-owner", "foreign-owner", "prepared", "wrong-parent", "changed-source", "paused"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Annotations = map[string]string{anycastBulkAnnotation: "true"}
			if mode == "disabled" {
				config.Annotations = nil
			}
			if mode == "invalid-flag" {
				config.Annotations[anycastBulkAnnotation] = "yes"
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
			gateway := &anycastGatewayExecutor{}
			deployment := &fabricGatewayDeployment{gateways: gateway}
			executor := &configurationGroup{parts: []configurationPart{{name: "fabric_sites", executor: deployment}}}
			err := a.bindAnycastBulk(config, source, executor)
			if mode == "invalid-flag" {
				if err == nil || gateway.bulkGuard != nil {
					t.Fatal("invalid flag enabled bulk")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "disabled" {
				if gateway.bulkGuard != nil {
					t.Fatal("default enabled bulk")
				}
				return
			}
			resource := anycastBulkPrefix + "1:01:" + strings.Repeat("a", 64)
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "fabric", Resource: "fabric_sites/" + resource, ParentID: "fabric", Action: "Create", Phase: "Submitting", SubmittedAt: &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
			if mode == "missing-owner" {
				nc.Status.Configuration.Owners = nil
			}
			if mode == "foreign-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			}
			if mode == "prepared" {
				op.Phase = "Prepared"
				op.SubmittedAt = nil
			}
			if mode == "wrong-parent" {
				op.ParentID = "other"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "changed-source" {
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "paused" {
				nc.Spec.Paused = true
				if err := a.statusWriter.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			err = gateway.bulkGuard(ctx, resource, "fabric")
			if (err == nil) != (mode == "valid") {
				t.Fatal("bulk guard outcome", err)
			}
		})
	}
}
