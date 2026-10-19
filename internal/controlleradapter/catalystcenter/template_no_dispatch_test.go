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

func TestTemplateNoDispatchEnvelope(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	before := metav1.NewTime(now.Add(-time.Minute))
	for _, test := range []struct {
		name   string
		mutate func(*cvk.NetworkControllerConfigurationOperation)
		valid  bool
	}{
		{"draft", func(*cvk.NetworkControllerConfigurationOperation) {}, true},
		{"prepared-draft", func(op *cvk.NetworkControllerConfigurationOperation) { op.PreparedAt = &before }, true},
		{"commit", func(op *cvk.NetworkControllerConfigurationOperation) {
			op.Resource = "objects/commit:Lab#base"
			op.Action = "Update"
			op.TargetID = "base"
		}, true},
		{"project", func(op *cvk.NetworkControllerConfigurationOperation) { op.Resource = "objects/Lab"; op.ParentID = "" }, true},
		{"builtin-project", func(op *cvk.NetworkControllerConfigurationOperation) {
			op.Resource = "Onboarding Configuration"
			op.ParentID = ""
		}, false},
		{"commit-create", func(op *cvk.NetworkControllerConfigurationOperation) { op.Resource = "commit:Lab#base" }, false},
		{"malformed-key", func(op *cvk.NetworkControllerConfigurationOperation) { op.Resource = "draft:Lab#base#extra" }, false},
		{"missing-parent", func(op *cvk.NetworkControllerConfigurationOperation) { op.ParentID = "" }, false},
		{"wrong-parent", func(op *cvk.NetworkControllerConfigurationOperation) { op.ParentID = "../unsafe" }, false},
		{"deployment", func(op *cvk.NetworkControllerConfigurationOperation) {
			op.Section = "inventory"
			op.Resource = "template-deploy:base/version/1/" + strings.Repeat("a", 64)
			op.TargetSerial = "SERIAL"
		}, false},
		{"membership", func(op *cvk.NetworkControllerConfigurationOperation) {
			op.Resource = "members/membership:key"
			op.Action = "Update"
			op.TargetID = "base"
		}, false},
		{"delete", func(op *cvk.NetworkControllerConfigurationOperation) { op.Action = "Delete"; op.TargetID = "base" }, false},
		{"task", func(op *cvk.NetworkControllerConfigurationOperation) { op.TaskID = "task" }, false},
		{"creator", func(op *cvk.NetworkControllerConfigurationOperation) { op.CreatedResourceID = "base" }, false},
		{"serial", func(op *cvk.NetworkControllerConfigurationOperation) { op.TargetSerial = "SERIAL" }, false},
		{"cancel", func(op *cvk.NetworkControllerConfigurationOperation) { op.CancellationRequestedAt = &now }, false},
		{"zero-floor", func(op *cvk.NetworkControllerConfigurationOperation) { op.PreparedAt = &metav1.Time{} }, false},
		{"future-floor", func(op *cvk.NetworkControllerConfigurationOperation) {
			later := metav1.NewTime(now.Add(time.Minute))
			op.PreparedAt = &later
		}, false},
		{"project-floor", func(op *cvk.NetworkControllerConfigurationOperation) {
			op.Resource = "Lab"
			op.ParentID = ""
			op.PreparedAt = &before
		}, false},
		{"missing-time", func(op *cvk.NetworkControllerConfigurationOperation) { op.SubmittedAt = nil }, false},
		{"missing-owner", func(op *cvk.NetworkControllerConfigurationOperation) { op.ConfigUID = "" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "templates", Resource: "draft:Lab#base", Action: "Create", ParentID: "project", Phase: "Submitting", SubmittedAt: &now}
			test.mutate(op)
			if noDispatchOperationSupported(op) != test.valid {
				t.Fatal("incorrect template eligibility")
			}
		})
	}
	// Empty tasks are never evidence without an original guard capability.
	write := &configurationWrite{resource: "draft:Lab#base", action: "Create", identity: configurationResourceIdentity{parent: "project"}, submit: func(context.Context) (Task, error) { return Task{}, nil }}
	if noDispatchWriteSupported("templates", write) {
		t.Fatal("missing guard accepted")
	}
	guardConfigurationPreflight(write, func(context.Context) error { return nil })
	if !noDispatchWriteSupported("templates", write) {
		t.Fatal("bound metadata guard refused")
	}
	write.identity.serial = "SERIAL"
	if noDispatchWriteSupported("templates", write) {
		t.Fatal("physical operation accepted")
	}
}
