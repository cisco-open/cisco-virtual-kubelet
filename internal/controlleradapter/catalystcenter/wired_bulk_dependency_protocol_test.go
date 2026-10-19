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

func TestWiredBulkDependencyProtocolPreservesLegacyAndRequiresEvidence(t *testing.T) {
	for _, mode := range []string{"verified", "changed-before-dispatch", "changed-during-guard", "missing-recovery-observer", "changed-after-acceptance", "empty-digest", "bad-digest", "nil-observer", "multiple-observers"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, members, f := newWiredBulkFixture(t)
			guard := func(context.Context, []Device) error { return nil }
			legacy, err := c.planWiredBulkProvision(ctx, "Global/Lab", members, guard)
			if err != nil || legacy == nil || !strings.HasPrefix(legacy.resource, wiredBulkProvisionPrefix+"v1:") {
				t.Fatal("legacy identity changed", err)
			}
			digest := strings.Repeat("a", 64)
			observer := wiredBulkDependencyObserver(func(context.Context) (string, error) { return digest, nil })
			if mode == "empty-digest" {
				digest = ""
			}
			if mode == "bad-digest" {
				digest = strings.Repeat("G", 64)
			}
			observers := []wiredBulkDependencyObserver{observer}
			if mode == "nil-observer" {
				observers[0] = nil
			}
			if mode == "multiple-observers" {
				observers = append(observers, observer)
			}
			if mode == "changed-during-guard" {
				guard = func(context.Context, []Device) error { digest = strings.Repeat("b", 64); return nil }
			}
			write, err := c.planWiredBulkProvision(ctx, "Global/Lab", members, guard, observers...)
			invalid := mode == "empty-digest" || mode == "bad-digest" || mode == "nil-observer" || mode == "multiple-observers"
			if invalid {
				if err == nil || write != nil || f.calls != 0 {
					t.Fatal("malformed dependency evidence admitted a writer", err)
				}
				return
			}
			if err != nil || write == nil || !strings.HasPrefix(write.resource, wiredBulkProvisionPrefix+"v2:") || write.resource == legacy.resource {
				t.Fatal("dependency evidence did not version native identity", err)
			}
			if _, _, _, err := parseWiredBulkResource(write.resource, write.action); err != nil || !validConfigurationGroupAction("inventory", write.resource, write.action) {
				t.Fatal("v2 escaped existing group classification", err)
			}
			if mode == "changed-before-dispatch" {
				digest = strings.Repeat("b", 64)
			}
			submitted := metav1.NewTime(time.Now().Add(-time.Second))
			task, err := write.submit(ctx)
			if mode == "changed-before-dispatch" || mode == "changed-during-guard" {
				if err == nil || f.calls != 0 {
					t.Fatal("stale dependency dispatched", err)
				}
				if _, err := write.submit(ctx); err == nil {
					t.Fatal("refused dependency closure replayed")
				}
				return
			}
			if err != nil || f.calls != 1 {
				t.Fatal("valid v2 native request not submitted once", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent, Phase: "Verifying", TaskID: task.ID, SubmittedAt: &submitted}
			if mode == "changed-after-acceptance" {
				digest = strings.Repeat("b", 64)
			}
			if mode == "missing-recovery-observer" {
				observers = nil
			}
			done, err := c.verifyWiredBulkProvision(ctx, "Global/Lab", members, op, time.Now(), observers...)
			if (err == nil && done) != (mode == "verified") {
				t.Fatal("task/record presence replaced dependency evidence", err)
			}
			if f.calls != 1 {
				t.Fatal("dependency recovery replayed a mutation")
			}
		})
	}
}
