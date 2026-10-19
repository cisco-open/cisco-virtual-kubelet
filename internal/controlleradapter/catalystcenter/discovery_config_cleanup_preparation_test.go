// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryConfigCleanupPreparation(t *testing.T) {
	exerciseDiscoveryCleanupPreparation(t, nil)
}

func exerciseDiscoveryCleanupPreparation(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, world := discoveryPruneFixture(t)
	if install != nil {
		install(a, c)
	}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{"test.cisco.vk/preparation-only"}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	o, err := a.observeDiscoveryConfigCleanup(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	_, source, err := a.discoveryCleanupInput(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	source.discoveryCleanup = &discoveryCleanupPreparation{observation: *o, candidate: o.Candidates[0]}
	resource := source.discoveryCleanup.resource()
	identity := []configurationResourceIdentity{{target: o.Candidates[0].ID}}
	if !discoveryCleanupPreparationAllowed(c, source, "inventory", resource, "Delete", identity) {
		t.Fatal("valid preparation refused")
	}
	for _, mode := range []string{"deletion", "spec", "generation", "version", "uid", "target", "action", "resource", "section", "extra-identity"} {
		t.Run(mode, func(t *testing.T) {
			copy := c.DeepCopy()
			ids := append([]configurationResourceIdentity(nil), identity...)
			r, action, section := resource, "Delete", "inventory"
			switch mode {
			case "deletion":
				copy.DeletionTimestamp = nil
			case "spec":
				copy.Spec.ModelSource.Exporter = "changed"
			case "generation":
				copy.Generation++
			case "version":
				copy.ResourceVersion += "1"
			case "uid":
				copy.UID = "foreign"
			case "target":
				ids[0].target = "foreign"
			case "action":
				action = "Create"
			case "resource":
				r = o.Candidates[0].Resource
			case "section":
				section = "sites"
			case "extra-identity":
				ids[0].parent = "foreign"
			}
			if discoveryCleanupPreparationAllowed(copy, source, section, r, action, ids) {
				t.Fatal("altered preparation authorized")
			}
			if _, err := a.prepareConfiguration(ctx, copy, source, section, r, action, ids...); err == nil {
				t.Fatal("altered preparation persisted")
			}
		})
	}
	// A controller ownership revision invalidates the observed plan. Restore
	// the original owner only after proving that stale preparation is refused.
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Owners[0].ConfigName = "foreign"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareConfiguration(ctx, c, source, "inventory", resource, "Delete", identity...); err == nil {
		t.Fatal("stale controller observation persisted")
	}
	nc.Status.Configuration.Owners[0].ConfigName = c.Name
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	before := journalState(t, a).DeepCopy()
	op, err := a.prepareDiscoveryConfigCleanup(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if op.Phase != "Prepared" || op.Resource != resource || op.TargetID != "42" || op.TaskID != "" || op.SubmittedAt != nil || op.CreatedResourceID != "" {
		t.Fatal("invalid Prepared record", op)
	}
	after := journalState(t, a).DeepCopy()
	if !reflect.DeepEqual(after.Operation, op) {
		t.Fatal("operation did not survive journal read")
	}
	after.Operation = nil
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preparation changed ownership receipts")
	}
	if _, err := a.prepareDiscoveryConfigCleanup(ctx, c); err == nil {
		t.Fatal("occupied journal replaced")
	}
	if err := a.startConfiguration(ctx, c, source, op.ID); err == nil {
		t.Fatal("preparation allowed native submission")
	}
	plainSource := *source
	plainSource.discoveryCleanup = nil
	if err := a.startConfiguration(ctx, c, &plainSource, op.ID); err == nil {
		t.Fatal("restart allowed native submission")
	}
	if !reflect.DeepEqual(journalState(t, a).Operation, op) || len(world.writes) != 0 {
		t.Fatal("preparation submitted or changed")
	}
}
