// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestConfigurationAtomicCompletionHook(t *testing.T) {
	for _, mode := range []string{"complete", "error", "no-write", "missing-receipt", "changed-operation", "duplicate-receipt", "wrong-phase", "wrong-section", "missing-hook", "local-copy", "stale-operation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", "action", "Create", configurationResourceIdentity{target: "device", serial: "serial"}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = "Verifying"
			nc.Status.Configuration.Operation.TaskID = "task"
			nc.Status.Configuration.Operation.SubmittedAt = &metav1.Time{Time: time.Now().Add(-time.Minute).UTC().Truncate(time.Second)}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation.DeepCopy()
			calls := 0
			plan := configurationApplyPlan{section: "inventory", recordCompletion: func(ctx context.Context, copy *cvk.NetworkControllerConfigurationOperation) error {
				calls++
				if mode == "error" {
					return errConfigurationBusy
				}
				if mode == "no-write" {
					return nil
				}
				current, err := a.currentController(ctx)
				if err != nil {
					return err
				}
				current.Status.Configuration.Operation.Phase = "Verified"
				if mode == "local-copy" {
					copy.Resource = "changed-copy"
				}
				if mode == "changed-operation" {
					current.Status.Configuration.Operation.TaskID = "other"
				}
				if mode != "missing-receipt" {
					if err := a.retainConfigurationActionReceipt(ctx, current, "native-result:uuid"); err != nil {
						return err
					}
				}
				if mode == "duplicate-receipt" {
					duplicate := current.Status.Configuration.ActionApplications[0]
					duplicate.Resource = "other-key"
					current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, duplicate)
				}
				return a.statusWriter.Status().Update(ctx, current)
			}}
			switch mode {
			case "wrong-phase":
				op.Phase = "Unknown"
			case "wrong-section":
				plan.section = "sites"
			case "missing-hook":
				plan.recordCompletion = nil
			case "stale-operation":
				op.TaskID = "other"
			}
			err = plan.completeWithReceipt(ctx, a, op)
			success := mode == "complete" || mode == "local-copy"
			if (err == nil) != success {
				t.Fatal("incorrect completion outcome", err)
			}
			if op.Resource != "action" {
				t.Fatal("callback changed caller journal")
			}
			blocked := mode == "wrong-phase" || mode == "wrong-section" || mode == "missing-hook" || mode == "stale-operation"
			if blocked && calls != 0 {
				t.Fatal("invalid operation reached callback")
			}
		})
	}
}
