// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Public reconciler with native HTTPS and simulated installed worker/lease/claim
// evidence. This qualifies routing and fence retention, not deployed actors.
func TestWiredBulkPublicAcknowledgedRecoveryRetainsConvergenceFence(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "running", "failed", "partial", "revoked", "changed-source", "pruned-group", "invalid-bulk-options", "removed-bulk-options"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			c, batchMembers, f := newWiredBulkFixture(t)
			a.client = c
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"REPROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			f.present["a"], f.present["b"] = "old-a", "old-b"
			for i := range batchMembers {
				batchMembers[i].state = "REPROVISION"
			}
			fixtureMembers := []configurationDispatchFixtureMember{}
			for i, name := range []string{"a", "b"} {
				address := "198.51.100.100"
				if i == 1 {
					address = "198.51.100.101"
				}
				serial := "serial-" + name
				device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: name, UID: types.UID(name + "-device"), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: address, PhysicalIdentity: serial}}
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: name, NodeUID: name + "-node", PhysicalIdentity: serial}
				node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-node")}}
				node.Status.NodeInfo.MachineID = serial
				for _, object := range []ctrlclient.Object{device, node} {
					if err := a.statusWriter.Create(ctx, object); err != nil {
						t.Fatal(err)
					}
				}
				fixtureMembers = append(fixtureMembers, configurationDispatchFixtureMember{device: device, node: node})
			}
			snapshot, targets, err := c.wiredBulkTargets(ctx, a.statusReader, config.Namespace, "Global/Lab", batchMembers)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			base := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: snapshot.resource(snapshot.mask), Action: "Update", TargetID: snapshot.anchor, ParentID: snapshot.anchor, Phase: "Prepared"}
			op, err := contract.WithGroupIdentity(base, targets)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			_, revoke := configurationGroupDispatchPermitFixture(t, a, fixtureMembers)
			installFabricGroupAuthorityFixture(t, a)
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = "Accepted"
			if mode == "unknown" {
				nc.Status.Configuration.Operation.Phase = "Unknown"
			}
			nc.Status.Configuration.Operation.TaskID = "batch-task"
			nc.Status.Configuration.Operation.SubmittedAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}
			if mode == "pruned-group" {
				nc.Status.Configuration.Operation.PhysicalGroup = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "running":
				f.taskMode = "running"
			case "failed":
				f.taskMode = "failed"
			case "partial":
				delete(f.present, "b")
			case "revoked":
				revoke()
			case "invalid-bulk-options", "removed-bulk-options":
				config.Annotations = map[string]string{wiredBulkAnnotation: "invalid"}
				if mode == "removed-bulk-options" {
					config.Annotations = nil
				}
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "changed-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			request := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
			for range 3 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, request); err != nil {
					t.Fatal("public recovery", err)
				}
			}
			state := journalState(t, a)
			expected := "Accepted"
			if mode == "unknown" {
				expected = "Unknown"
			}
			success := mode == "accepted" || mode == "unknown" || mode == "invalid-bulk-options" || mode == "removed-bulk-options"
			if success {
				expected = "Verifying"
			}
			if state.Operation == nil || state.Operation.ID != op.ID || state.Operation.Phase != expected || state.Operation.TaskID != "batch-task" || len(state.ActionApplications) != 0 || f.calls != 0 {
				t.Fatal("public recovery replayed, advanced or lost fence", mode, state.Operation)
			}
			var current configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, request.NamespacedName, &current); err != nil {
				t.Fatal(err)
			}
			if success {
				found := false
				for _, condition := range current.Status.Conditions {
					if condition.Reason == "GroupConvergenceVerificationUnavailable" {
						found = true
					}
				}
				if !found || current.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("missing explicit convergence gate", current.Status)
				}
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if len(reservations.Items) != 2 {
				t.Fatal("member set changed")
			}
			for _, member := range reservations.Items {
				if member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Claim == nil || member.Status.Grant.Phase == "Released" {
					t.Fatal("public observation completed/released member")
				}
			}
		})
	}
}
