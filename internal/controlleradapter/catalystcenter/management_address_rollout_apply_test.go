// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type managementRolloutWriter struct {
	ctrlclient.Client
	patches  int
	conflict bool
}

func (w *managementRolloutWriter) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.PatchOption) error {
	w.patches++
	data, err := patch.Data(obj)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var metadata map[string]json.RawMessage
	var spec map[string]json.RawMessage
	if json.Unmarshal(fields["metadata"], &metadata) != nil || json.Unmarshal(fields["spec"], &spec) != nil || len(fields) != 2 || len(spec) != 1 || spec["address"] == nil || metadata["resourceVersion"] == nil {
		return errors.New("patch is not restricted to address with a version precondition")
	}
	if w.conflict {
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "ciscodevices"}, obj.GetName(), errors.New("concurrent change"))
	}
	return w.Client.Patch(ctx, obj, patch, opts...)
}

func TestManagementAddressDeclarationApply(t *testing.T) {
	for _, mode := range []string{"apply", "denied", "no-authority", "uncordoned", "alias", "stale-reservation", "device-race", "node-race", "controller-race", "receipt-race", "late-alias", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			ref, before, op, r := managementAddressBaselineFixture(t)
			now := op.SubmittedAt.Time.Add(time.Second)
			op.Phase = "Verified"
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "catc", UID: "controller", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: TypeName}}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy(), ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: op.Resource, Operation: *op.DeepCopy()}}}
			claimed := metav1.NewTime(op.SubmittedAt.Add(-10 * time.Second))
			r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: claimed}
			r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Released", GrantedAt: claimed}
			completion, err := contract.CompletionForReceipt(r, nc, now)
			if err != nil {
				t.Fatal(err)
			}
			r.Status.Completion = completion
			settled := metav1.NewTime(now)
			r.Status.ManagerSettledAt = &settled
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: before.before, PhysicalIdentity: "serial"}}
			device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeName: "switch", NodeUID: "node", PhysicalIdentity: "serial"}
			node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node"}}
			node.Spec.Unschedulable = mode != "uncordoned"
			node.Status.NodeInfo.MachineID = "serial"
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{cvk.AddToScheme, ops.AddToScheme, core.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc, r, device, node).Build()
			if err := c.Get(ctx, ctrlclient.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			if mode == "stale-reservation" {
				r.ResourceVersion = "stale"
			}
			makeAlias := func() {
				alias := device.DeepCopy()
				alias.Namespace = "other"
				alias.Name = "alias"
				alias.UID = "alias"
				alias.ResourceVersion = ""
				if err := c.Create(ctx, alias); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "alias" {
				makeAlias()
			}
			calls := 0
			authorize := managementAddressRolloutAuthorization(func(_ context.Context, n *cvk.NetworkController, reservation *ops.ControllerConfigurationReservation, d *cvk.CiscoDevice, nd *core.Node) error {
				calls++
				switch mode {
				case "denied":
					return errors.New("destination proof denied")
				case "device-race":
					d.Annotations = map[string]string{"concurrent": "change"}
					if err := c.Update(ctx, d); err != nil {
						t.Fatal(err)
					}
				case "node-race":
					nd.Spec.Unschedulable = false
					if err := c.Update(ctx, nd); err != nil {
						t.Fatal(err)
					}
				case "controller-race":
					n.Spec.Paused = true
					if err := c.Update(ctx, n); err != nil {
						t.Fatal(err)
					}
				case "receipt-race":
					reservation.Status.Completion = nil
					if err := c.Update(ctx, reservation); err != nil {
						t.Fatal(err)
					}
				case "late-alias":
					makeAlias()
				}
				// Authorizers receive copies, so accidental edits cannot alter the patch.
				d.Spec.Address = "198.51.100.254"
				return nil
			})
			if mode == "no-authority" {
				authorize = nil
			}
			writer := &managementRolloutWriter{Client: c, conflict: mode == "conflict"}
			err = applyManagementAddressDeclaration(ctx, c, writer, r, ref, now, authorize)
			var actual cvk.CiscoDevice
			if getErr := c.Get(ctx, ctrlclient.ObjectKeyFromObject(device), &actual); getErr != nil {
				t.Fatal(getErr)
			}
			if mode != "apply" {
				if err == nil || actual.Spec.Address != before.before {
					t.Fatal("refused rollout changed declaration", err)
				}
				if mode == "conflict" {
					if !apierrors.IsConflict(err) || writer.patches != 1 || calls != 1 {
						t.Fatal("conflict retried or hidden", err)
					}
				} else if writer.patches != 0 {
					t.Fatal("unsafe patch attempted")
				}
				return
			}
			if err != nil || actual.Spec.Address != ref.IP || writer.patches != 1 || calls != 1 {
				t.Fatal("authorized rollout not applied", err)
			}
			actual.Spec.Address = before.before
			actual.ResourceVersion = device.ResourceVersion
			if !reflect.DeepEqual(&actual, device) {
				t.Fatal("rollout modified unrelated fields")
			}
			if err := applyManagementAddressDeclaration(ctx, c, writer, r, ref, now, authorize); err == nil || writer.patches != 1 {
				t.Fatal("settled old-baseline operation replayed declaration update", err)
			}
			var retained core.Node
			if err := c.Get(ctx, ctrlclient.ObjectKeyFromObject(node), &retained); err != nil || !retained.Spec.Unschedulable {
				t.Fatal("rollout lost cordon", err)
			}
		})
	}
}
