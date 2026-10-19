// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricDevicesBulkDeferredParentPreflightIsNotAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-fabric", "undeclared-parent", "wrong-role", "bad-body", "missing-managed-parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, roots, f, api := newManagedFabricDevicesBulkFixture(t, "valid")
			root, err := c.fabricDevicesBulkSnapshot(ctx, roots)
			if err != nil {
				t.Fatal(err)
			}
			proposal := &fabricDevicesParentProposal{fabricID: root.fabric, members: map[string]fabricDeviceConfiguration{}}
			for _, entry := range root.entries {
				proposal.members[entry.target.ID] = entry.native
			}
			zones := append([]fabricDeviceIntent(nil), roots...)
			for i := range zones {
				zones[i].fabricZone = "Global/Lab/Building"
			}
			if _, _, err := c.fabricDevicesBulkTargets(ctx, api, "lab", zones); err == nil {
				t.Fatal("normal zone observer accepted absent native parents")
			}
			switch mode {
			case "wrong-fabric":
				proposal.fabricID = "other"
			case "undeclared-parent":
				delete(proposal.members, "edge-1")
			case "wrong-role":
				row := proposal.members["edge-1"]
				row.Roles = []string{"CONTROL_PLANE_NODE"}
				proposal.members["edge-1"] = row
			case "bad-body":
				row := proposal.members["edge-1"]
				row.NetworkDeviceID = "other"
				proposal.members["edge-1"] = row
			case "missing-managed-parent":
				var device cvk.CiscoDevice
				if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "edge0"}, &device); err != nil {
					t.Fatal(err)
				}
				if err := api.Delete(ctx, &device); err != nil {
					t.Fatal(err)
				}
				zones = zones[1:]
			}
			native, targets, err := c.fabricDevicesBulkPreflightTargets(ctx, api, "lab", zones, proposal)
			if mode == "valid" {
				if err != nil || !native.deferredParent || len(targets) != 3 {
					t.Fatal("prospective complete footprint", err)
				}
				for _, entry := range native.entries {
					if !entry.deferredParent {
						t.Fatal("missing parent concealed in preflight")
					}
				}
			} else if err == nil {
				t.Fatal("invalid proposal authorized prospective zone", mode)
			}
			write, _, err := c.planManagedFabricDevicesBulk(ctx, api, "lab", zones, func(context.Context, []Device) error { return nil })
			if err == nil || write != nil || f.posts != 0 || len(f.created) != 0 {
				t.Fatal("proposal became native planning authority")
			}
		})
	}
}

func TestFabricDevicesBulkDeferredParentsRequireFreshNativeReadback(t *testing.T) {
	ctx := context.Background()
	c, roots, f, api := newManagedFabricDevicesBulkFixture(t, "deferred-parent")
	root, err := c.fabricDevicesBulkSnapshot(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	proposal := &fabricDevicesParentProposal{fabricID: root.fabric, members: map[string]fabricDeviceConfiguration{}}
	for _, entry := range root.entries {
		proposal.members[entry.target.ID] = entry.native
	}
	zones := append([]fabricDeviceIntent(nil), roots...)
	for i := range zones {
		zones[i].fabricZone = "Global/Lab/Building"
	}
	prospective, _, err := c.fabricDevicesBulkPreflightTargets(ctx, api, "lab", zones, proposal)
	if err != nil || !prospective.deferredParent {
		t.Fatal("missing parents not represented prospectively", err)
	}
	// Only the fixture's native readback changes here. This does not simulate
	// completed device proof, reservation settlement or installed public dispatch.
	for i, entry := range root.entries {
		row := entry.native
		row.ID = "parent-" + entry.target.ID
		f.parents[entry.target.ID] = row
		if i == 0 {
			partial, _, err := c.fabricDevicesBulkPreflightTargets(ctx, api, "lab", zones, proposal)
			if err != nil || !partial.deferredParent {
				t.Fatal("partial native parents cleared deferral", err)
			}
			if write, _, err := c.planManagedFabricDevicesBulk(ctx, api, "lab", zones, func(context.Context, []Device) error { return nil }); err == nil || write != nil {
				t.Fatal("partial parent readback enabled complete zone batch")
			}
		}
	}
	actual, _, err := c.fabricDevicesBulkTargets(ctx, api, "lab", zones)
	if err != nil || actual.deferredParent {
		t.Fatal("fresh native parents unavailable", err)
	}
	resolved, _, err := c.fabricDevicesBulkPreflightTargets(ctx, api, "lab", zones, proposal)
	if err != nil || resolved.deferredParent || actual.resource(actual.mask) != resolved.resource(resolved.mask) {
		t.Fatal("resolved proposal changed normal identity", err)
	}
	if actual.resource(actual.mask) == prospective.resource(prospective.mask) {
		t.Fatal("prospective identity reused as actual zone identity")
	}
	write, _, err := c.planManagedFabricDevicesBulk(ctx, api, "lab", zones, func(context.Context, []Device) error { return nil })
	if err != nil || write == nil {
		t.Fatal("actual parent-backed private planning", err)
	}
	task, err := write.submit(ctx)
	if err != nil || task.ID != "task" || f.posts != 1 {
		t.Fatal("native zone request", err)
	}
	op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", TaskID: task.ID, Action: "Create", TargetID: "zone", ParentID: "zone", Resource: write.resource}
	if done, err := c.verifyFabricDevicesBulk(ctx, zones, op); err != nil || !done {
		t.Fatal("actual zone readback", err)
	}
	op.Resource = prospective.resource(prospective.mask)
	if done, _ := c.verifyFabricDevicesBulk(ctx, zones, op); done {
		t.Fatal("prospective observation accepted as final membership evidence")
	}
}
