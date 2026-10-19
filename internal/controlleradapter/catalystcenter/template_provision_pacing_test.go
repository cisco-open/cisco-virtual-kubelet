// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestTemplateProvisionSettlingDurableEvidence(t *testing.T) {
	for _, mode := range []string{"settled", "recent", "boundary", "planning", "existing", "existing-recent", "legacy", "future-prepared", "foreign-owner", "foreign-receipt", "duplicate", "wrong-serial", "wrong-site", "future-verification", "old-revision", "source-changed", "wrong-write", "submitting", "accepted", "second-device-recent", "second-device-missing"} {
		t.Run(mode, func(t *testing.T) {
			a, config := journalFixture(t)
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			source := &configSourceSnapshot{uid: "source", version: "1"}
			receiptOp := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: string(config.UID), ConfigName: config.Name, ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: "source", SourceVersion: "1", Section: "inventory", Resource: "wired-provision:" + strings.Repeat("b", 64), Action: "Create", TargetID: "device", TargetSerial: "serial", ParentID: "site", Phase: "Verified", TaskID: "task", SubmittedAt: &metav1.Time{Time: now.Add(-time.Minute)}}
			receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: receiptOp.Resource, Operation: receiptOp, VerifiedAt: &metav1.Time{Time: now.Add(-11 * time.Second)}}
			write := &configurationWrite{resource: "template-deploy:base/version/1/" + strings.Repeat("c", 64), action: "Create", identity: configurationResourceIdentity{target: "device", serial: "serial", parent: "base"}}
			op := receiptOp.DeepCopy()
			op.Resource = write.resource
			op.Action = write.action
			op.ParentID = "base"
			op.Phase = "Prepared"
			op.TaskID = ""
			op.SubmittedAt = nil
			op.PreparedAt = &metav1.Time{Time: now.Add(-11 * time.Second)}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{receipt}, Operation: op}
			devices := map[string]wiredInventoryBinding{"edge": {deviceID: "device", serial: "serial", siteID: "site"}}
			switch mode {
			case "recent":
				nc.Status.Configuration.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(-9 * time.Second)}
			case "boundary":
				nc.Status.Configuration.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(-10 * time.Second)}
			case "planning":
				nc.Status.Configuration.Operation = nil
				nc.Status.Configuration.ActionApplications = nil
			case "existing":
				nc.Status.Configuration.ActionApplications = nil
			case "existing-recent":
				nc.Status.Configuration.ActionApplications = nil
				op.PreparedAt = &metav1.Time{Time: now.Add(-9 * time.Second)}
			case "legacy":
				nc.Status.Configuration.ActionApplications = nil
				op.PreparedAt = nil
			case "future-prepared":
				nc.Status.Configuration.ActionApplications = nil
				op.PreparedAt = &metav1.Time{Time: now.Add(time.Second)}
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "other"
			case "foreign-receipt":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigUID = "other"
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, receipt)
			case "wrong-serial":
				nc.Status.Configuration.ActionApplications[0].Operation.TargetSerial = "replacement"
			case "wrong-site":
				nc.Status.Configuration.ActionApplications[0].Operation.ParentID = "other"
			case "future-verification":
				nc.Status.Configuration.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(time.Second)}
			case "old-revision":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigGeneration--
			case "source-changed":
				op.SourceVersion = "2"
			case "wrong-write":
				op.Resource = "template-deploy:different"
			case "submitting":
				op.Phase = "Submitting"
				op.SubmittedAt = &metav1.Time{Time: now}
			case "accepted":
				op.Phase = "Accepted"
				op.TaskID = "accepted-task"
			case "second-device-recent", "second-device-missing":
				devices["second"] = wiredInventoryBinding{deviceID: "second", serial: "second-serial", siteID: "site"}
				if mode == "second-device-recent" {
					other := receipt
					other.Operation = *receiptOp.DeepCopy()
					other.Resource = "wired-provision:" + strings.Repeat("d", 64)
					other.Operation.Resource = other.Resource
					other.Operation.TargetID = "second"
					other.Operation.TargetSerial = "second-serial"
					other.VerifiedAt = &metav1.Time{Time: now.Add(-time.Second)}
					nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, other)
				}
			}
			err = templateProvisionDependenciesSettled(nc, config, source, devices, write, now)
			allowed := mode == "settled" || mode == "planning" || mode == "existing" || mode == "old-revision" || mode == "submitting" || mode == "second-device-missing"
			if allowed && err != nil || !allowed && err == nil {
				t.Fatal("incorrect provision settling result", err)
			}
			if (mode == "recent" || mode == "boundary" || mode == "legacy" || mode == "existing-recent" || mode == "second-device-recent") && !errors.Is(err, errConfigurationBusy) {
				t.Fatal("did not retain pending interval", err)
			}
			// Reconstructing both objects preserves the same timer decision.
			again := templateProvisionDependenciesSettled(nc.DeepCopy(), config.DeepCopy(), source, devices, write, now)
			if !errors.Is(again, err) {
				t.Fatal("restart changed settling decision", again, err)
			}
		})
	}
}

func TestTemplateProvisionWaitPreparationDoesNotAuthorizePhysicalWrite(t *testing.T) {
	for _, mode := range []string{"physical-template", "physical-provision", "inventory-without-serial", "metadata-template"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			section, resource := "inventory", "template-deploy:base/version/1/"+strings.Repeat("d", 64)
			identity := configurationResourceIdentity{target: "device", parent: "base", serial: "FOC12345678", preparationWait: true}
			switch mode {
			case "physical-provision":
				resource = "wired-provision:" + strings.Repeat("e", 64)
			case "inventory-without-serial":
				identity.serial = ""
			case "metadata-template":
				section = "templates"
				resource = "draft:Lab#combined"
				identity.serial = ""
			}
			config.Spec.ManagedSections = []string{section}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, nil, section, resource, "Create", identity)
			allowed := mode == "physical-template" || mode == "metadata-template"
			if !allowed {
				if err == nil {
					t.Fatal("unsupported preparation wait accepted")
				}
				return
			}
			if err != nil || op == nil || op.PreparedAt == nil || op.PreparedAt.IsZero() {
				t.Fatal("missing persisted observation floor", err)
			}
			if mode == "physical-template" {
				calls := 0
				write := &configurationWrite{resource: resource, action: "Create", identity: identity, submit: func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }}
				if err := a.resumePhysicalConfigurationWrite(ctx, config, nil, op, section, write); err == nil || calls != 0 {
					t.Fatal("timestamp bypassed physical admission")
				}
				held := journalState(t, a).Operation
				if held == nil || held.ID != op.ID || held.Phase != "Prepared" || held.TaskID != "" || held.SubmittedAt != nil {
					t.Fatal("denied dispatch changed preparation", held)
				}
			}
		})
	}
}
