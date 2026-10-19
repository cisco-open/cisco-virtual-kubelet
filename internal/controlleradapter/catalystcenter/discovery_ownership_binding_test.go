// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestDiscoveryMixedDispatchRequiresAllOwners(t *testing.T) {
	for _, mode := range []string{"stable", "inventory-owner-pruned", "template-owner-pruned", "foreign-inventory-owner", "receipt-pruned"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := discoveryPruneFixture(t)
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
			config.Spec.ManagedSections = []string{"templates", "inventory"}
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(discoveryModel()), `"SSH"`, `"SSH,TELNET"`, 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigUID: string(config.UID), ConfigName: config.Name})
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			intent, err := parseInventoryIntent(source.raw)
			if err != nil {
				t.Fatal(err)
			}
			// Isolate binding/dispatch authority; full source composition is exercised by
			// the public mixed lifecycle tests in both section orders.
			plan := configurationApplyPlan{section: "templates", executor: &configurationGroup{parts: []configurationPart{{name: "discoveries", executor: &discoveryExecutor{client: a.client, desired: intent.discoveries}}}}}
			if err := a.bindDiscoveryOwnership(nc, config, source, &plan); err != nil {
				t.Fatal(err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil || write.action != "Delete" || !strings.HasPrefix(write.resource, "discoveries/"+ownedDiscoveryPrefix) {
				t.Fatal("bound replacement plan", err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, plan.section, write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, source, op.ID, write.identity); err != nil {
				t.Fatal(err)
			}
			nc, _ = a.currentController(ctx)
			switch mode {
			case "inventory-owner-pruned":
				nc.Status.Configuration.Owners = nc.Status.Configuration.Owners[1:]
			case "template-owner-pruned":
				nc.Status.Configuration.Owners = nc.Status.Configuration.Owners[:1]
			case "foreign-inventory-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "other"
			case "receipt-pruned":
				nc.Status.Configuration.ActionApplications = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			_, err = write.submit(ctx)
			expected := 0
			if mode == "stable" {
				expected = 1
			}
			if (err == nil) != (expected == 1) || len(world.writes) != expected {
				t.Fatal("partial owner/result allowed write or stable scope refused", mode, err, world.writes)
			}
			if _, err := write.submit(ctx); err == nil || len(world.writes) != expected {
				t.Fatal("consumed mixed plan replayed")
			}
		})
	}
}

func TestDiscoveryMixedNamespaceAndCallbackDelegation(t *testing.T) {
	a, config, _ := discoveryPruneFixture(t)
	nc, _ := a.currentController(context.Background())
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	config.Spec.ManagedSections = []string{"inventory", "templates"}
	desired, err := parseInventoryIntent(discoveryModel())
	if err != nil {
		t.Fatal(err)
	}
	observed, recorded, recovered := false, false, false
	prior := configurationApplyPlan{section: "templates", executor: &configurationGroup{parts: []configurationPart{{name: "discoveries", executor: &discoveryExecutor{client: a.client, desired: desired.discoveries}}}}, observeCompletion: func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
		observed = true
		return true, nil
	}, recordCompletion: func(context.Context, *cvk.NetworkControllerConfigurationOperation) error { recorded = true; return nil }, receiptOperation: func(op *cvk.NetworkControllerConfigurationOperation) bool { return op.Resource == "templates/private" }, recoverKnownTask: func(op *cvk.NetworkControllerConfigurationOperation) bool {
		recovered = true
		return op.Resource == "templates/private"
	}}
	if err := a.bindDiscoveryOwnership(nc, config, &configSourceSnapshot{raw: discoveryModel()}, &prior); err != nil {
		t.Fatal(err)
	}
	other := &cvk.NetworkControllerConfigurationOperation{Section: "templates", Resource: "templates/private", Phase: "Unknown"}
	if !prior.receiptOperation(other) || !prior.canObserveUnknownTask(other) || !recovered {
		t.Fatal("prior receipt/recovery selector lost")
	}
	if _, err := prior.observeCompletion(context.Background(), other, time.Now()); err != nil || !observed {
		t.Fatal("prior observer lost")
	}
	if err := prior.recordCompletion(context.Background(), other); err != nil || !recorded {
		t.Fatal("prior writer lost")
	}
	r := nc.Status.Configuration.ActionApplications[0]
	grouped := r.Operation.DeepCopy()
	grouped.Section = "templates"
	grouped.Resource = "discoveries/" + grouped.Resource
	if !ownedDiscoveryOperation(grouped) || discoveryOperationNameHash(grouped.Resource) != discoveryOperationNameHash(r.Operation.Resource) {
		t.Fatal("canonical namespace not preserved")
	}
	for _, bad := range []string{"other/" + r.Operation.Resource, "discoveries/discoveries/" + r.Operation.Resource} {
		op := grouped.DeepCopy()
		op.Resource = bad
		if ownedDiscoveryOperation(op) {
			t.Fatal("unqualified namespace accepted")
		}
	}
	grouped.Resource = discoveryLogicalResource(grouped.Resource)
	if ownedDiscoveryOperation(grouped) {
		t.Fatal("root discovery resource accepted in templates journal")
	}
}

func TestDiscoveryDuplicateLogicalReceiptAcrossNamespaces(t *testing.T) {
	a, config, world := discoveryPruneFixture(t)
	nc, _ := a.currentController(context.Background())
	original := nc.Status.Configuration.ActionApplications[0]
	duplicate := *original.DeepCopy()
	duplicate.Section = "templates"
	duplicate.Operation.Section = "templates"
	duplicate.Operation.Resource = "discoveries/" + duplicate.Operation.Resource
	duplicate.Operation.ID = strings.Repeat("d", 32)
	if !validOwnedDiscoveryReceipt(duplicate) {
		t.Fatal("canonical alternate-namespace fixture invalid")
	}
	state := nc.Status.Configuration.DeepCopy()
	state.ActionApplications = append(state.ActionApplications, duplicate)
	if ownedDiscoveryReceiptPresent(state, &original.Operation) {
		t.Fatal("duplicate logical resource allowed journal release")
	}
	op := duplicate.Operation.DeepCopy()
	op.Action = "Delete"
	op.TargetID = "42"
	if err := ownedDiscoveryCapacity(state, op); err == nil {
		t.Fatal("duplicate logical resource allowed capacity reservation")
	}
	intent, err := parseInventoryIntent(discoveryModel())
	if err != nil {
		t.Fatal(err)
	}
	executor := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client, desired: intent.discoveries}, owner: string(config.UID), receipts: state.ActionApplications}
	if _, err := executor.next(context.Background()); err == nil || len(world.writes) != 0 {
		t.Fatal("duplicate ownership was ignored by native planning")
	}
}

func TestDiscoveryPureDispatchHonorsOverlappingTemplateOwner(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			ctx := context.Background()
			a, config, world := discoveryPruneFixture(t)
			nc, _ := a.currentController(ctx)
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			plan, handled, err := a.compileOwnedDiscoveryApply(nc, config, source)
			if err != nil || !handled {
				t.Fatal(err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, plan.section, write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, source, op.ID, write.identity); err != nil {
				t.Fatal(err)
			}
			nc, _ = a.currentController(ctx)
			owner := cvk.NetworkControllerSectionOwner{Section: "templates", ConfigUID: string(config.UID), ConfigName: config.Name}
			if foreign {
				owner.ConfigUID = "foreign"
			}
			nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, owner)
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			_, err = write.submit(ctx)
			expected := 1
			if foreign {
				expected = 0
			}
			if (err == nil) != !foreign || len(world.writes) != expected {
				t.Fatal("overlapping owner invariant not enforced", err, world.writes)
			}
		})
	}
}
