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

func TestWiredProvisionRetainedRevision(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	resource := "wired-provision:" + strings.Repeat("a", 64)
	identity := configurationResourceIdentity{target: "device", parent: "site", serial: "serial"}
	op, err := a.prepareConfiguration(ctx, config, nil, "inventory", resource, "Update", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"Accepted", "Verifying", "Verified"} {
		task := ""
		if phase == "Accepted" {
			task = "task"
		}
		if err := a.advanceConfiguration(ctx, op.ID, phase, task); err != nil {
			t.Fatal(err)
		}
	}
	state := journalState(t, a)
	desired := state.Operation.DeepCopy()
	// This is the receipt actually emitted by the shared physical-action journal.
	if len(state.ActionApplications) != 1 {
		t.Fatal("missing shared receipt")
	}
	now := time.Now()
	if applied, err := wiredProvisionAlreadyApplied(state, desired, now); err != nil || !applied {
		t.Fatal(applied, err)
	}
	for _, mode := range []string{"duplicate", "foreign-owner", "no-owner", "conflicting-owner", "bad-phase", "missing-task", "missing-submission", "missing-verification", "future-verification", "early-verification", "wrong-resource", "wrong-section", "bad-operation", "wrong-device", "wrong-serial", "wrong-site", "generation", "controller-generation", "source", "source-version", "action"} {
		t.Run(mode, func(t *testing.T) {
			current := state.DeepCopy()
			want := desired.DeepCopy()
			receipt := &current.ActionApplications[0]
			switch mode {
			case "duplicate":
				current.ActionApplications = append(current.ActionApplications, *receipt)
			case "foreign-owner":
				receipt.Operation.ConfigUID = "other"
			case "no-owner":
				current.Owners = nil
			case "conflicting-owner":
				owner := current.Owners[0]
				owner.ConfigUID = "other"
				current.Owners = append(current.Owners, owner)
			case "bad-phase":
				receipt.Operation.Phase = "Unknown"
			case "missing-task":
				receipt.Operation.TaskID = ""
			case "missing-submission":
				receipt.Operation.SubmittedAt = nil
			case "missing-verification":
				receipt.VerifiedAt = nil
			case "future-verification":
				stamp := metav1.NewTime(now.Add(time.Hour))
				receipt.VerifiedAt = &stamp
			case "early-verification":
				stamp := metav1.NewTime(receipt.Operation.SubmittedAt.Add(-time.Hour))
				receipt.VerifiedAt = &stamp
			case "wrong-resource":
				receipt.Operation.Resource = "other"
			case "wrong-section":
				receipt.Operation.Section = "fabric"
			case "bad-operation":
				receipt.Operation.ID = "bad"
			case "wrong-device":
				want.TargetID = "other"
			case "wrong-serial":
				want.TargetSerial = "other"
			case "wrong-site":
				want.ParentID = "other"
			case "generation":
				want.ConfigGeneration++
			case "controller-generation":
				want.ControllerGeneration++
			case "source":
				want.SourceUID = "other"
			case "source-version":
				want.SourceVersion = "new"
			case "action":
				want.Action = "Create"
			}
			applied, err := wiredProvisionAlreadyApplied(current, want, now)
			if applied {
				t.Fatal("invalid/stale revision suppressed action")
			}
			invalid := mode == "duplicate" || mode == "foreign-owner" || mode == "no-owner" || mode == "conflicting-owner" || strings.HasPrefix(mode, "missing-") || mode == "bad-phase" || mode == "future-verification" || mode == "early-verification" || mode == "wrong-resource" || mode == "wrong-section" || mode == "bad-operation"
			if (err != nil) != invalid {
				t.Fatal("wrong receipt classification", err)
			}
		})
	}
	for _, resource := range []string{"wired-provision:", "wired-provision:" + strings.Repeat("A", 64), "wired-provision:" + strings.Repeat("g", 64), "wireless-provision:" + strings.Repeat("a", 64)} {
		want := desired.DeepCopy()
		want.Resource = resource
		if _, err := wiredProvisionAlreadyApplied(nil, want, now); err == nil {
			t.Fatal("invalid desired resource")
		}
	}
	if applied, err := wiredProvisionAlreadyApplied(&cvk.NetworkControllerConfigurationStatus{}, desired, now); err != nil || applied {
		t.Fatal(applied, err)
	}
}
