// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Read faults inject observations only; this helper has no Kubernetes writer or
// appliance transport. Cluster list scoping is explicitly checked.
type metadataScopeReader struct {
	client.Reader
	mode                                                       string
	devices, nodes, controllers, reservations, leases, configs int
}

func (r *metadataScopeReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*coordination.Lease); ok {
		r.leases++
		if r.mode == "lease-denied" {
			return errors.New("lease read forbidden")
		}
		if r.mode == "late-fence" && r.leases > 2 {
			lease := obj.(*coordination.Lease)
			lease.Name = key.Name
			lease.Namespace = key.Namespace
			return nil // Even malformed evidence at this key must hold the scope.
		}
	}
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if intent, ok := obj.(*config.NetworkControllerConfig); ok {
		r.configs++
		if r.mode == "late-source" && r.configs >= 3 {
			intent.Generation++
		}
	}
	return nil
}
func (r *metadataScopeReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	options := &client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(options)
	}
	if options.Namespace != "" || options.LabelSelector != nil || options.FieldSelector != nil {
		return errors.New("metadata scope requires complete global lists")
	}
	if r.mode == "list-denied" {
		return errors.New("global lists forbidden")
	}
	if _, ok := list.(*ops.ControllerConfigurationReservationList); ok && r.mode == "reservation-denied" {
		return errors.New("reservation list forbidden")
	}
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	switch rows := list.(type) {
	case *cvk.CiscoDeviceList:
		r.devices++
		if r.mode == "late-device" && r.devices == 2 {
			rows.Items = append(rows.Items, cvk.CiscoDevice{Spec: cvk.DeviceSpec{PhysicalIdentity: "SECOND123"}})
		}
	case *core.NodeList:
		r.nodes++
		if r.mode == "late-node" && r.nodes == 2 {
			rows.Items = append(rows.Items, core.Node{Status: core.NodeStatus{NodeInfo: core.NodeSystemInfo{MachineID: "SECOND123"}}})
		}
		if r.mode == "late-unknown-node" && r.nodes == 2 {
			rows.Items = append(rows.Items, core.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{managedprotocol.AnnotationManaged: "true"}}})
		}
	case *cvk.NetworkControllerList:
		r.controllers++
	case *ops.ControllerConfigurationReservationList:
		r.reservations++
		if r.mode == "late-reservation" && r.reservations == 2 {
			rows.Items = append(rows.Items, ops.ControllerConfigurationReservation{Spec: ops.ControllerConfigurationReservationSpec{PhysicalIdentity: "SECOND123"}})
		}
	}
	return nil
}

func TestUnmanagedMetadataScope(t *testing.T) {
	modes := []string{"valid", "unrelated", "unknown-device", "invalid-device", "device-first", "device-second", "retained-identity", "node-second", "other-operation", "other-group", "reservation", "settled-reservation", "deleting-reservation", "malformed-reservation", "fence", "late-device", "late-node", "late-unknown-node", "late-reservation", "late-fence", "late-source", "list-denied", "reservation-denied", "lease-denied", "foreign-owner", "stale-controller", "noncanonical", "duplicate", "empty", "unknown-namespace", "physical-operation", "submitted", "task", "update"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, _ := bootstrapIdentityFixture(t)
			_ = ops.AddToScheme(c.Scheme())
			_ = coordination.AddToScheme(c.Scheme())
			op := nc.Status.Configuration.Operation
			op.Action = "Delete"
			op.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
			op.TargetSerial = ""
			if mode == "physical-operation" {
				op.TargetSerial = "bootstrap123"
			}
			if mode == "submitted" {
				op.Phase = "Submitting"
			}
			if mode == "task" {
				op.TaskID = "native-task"
			}
			if mode == "update" {
				op.Action = "Update"
			}
			if mode == "foreign-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "other"
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if mode == "device-first" || mode == "device-second" || mode == "retained-identity" || mode == "unrelated" || mode == "unknown-device" || mode == "invalid-device" {
				serial := "SECOND123"
				if mode == "device-first" {
					serial = "BOOTSTRAP123"
				}
				if mode == "unrelated" {
					serial = "different"
				}
				d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "other-tenant"}, Spec: cvk.DeviceSpec{PhysicalIdentity: serial}}
				if mode == "retained-identity" {
					d.Spec.PhysicalIdentity = "different"
					d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{PhysicalIdentity: "SECOND123"}
				}
				if mode == "unknown-device" {
					d.Spec.PhysicalIdentity = ""
				}
				if mode == "invalid-device" {
					d.Spec.PhysicalIdentity = "bad serial"
				}
				if err := c.Create(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "node-second" {
				if err := c.Create(ctx, &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "orphan"}, Status: core.NodeStatus{NodeInfo: core.NodeSystemInfo{SystemUUID: "SECOND123"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "other-operation" || mode == "other-group" {
				other := nc.DeepCopy()
				other.Name = "elsewhere"
				other.Namespace = "other-tenant"
				other.UID = "other"
				other.ResourceVersion = ""
				other.Status.Configuration.Operation.TargetSerial = "SECOND123"
				if mode == "other-group" {
					other.Status.Configuration.Operation.TargetSerial = ""
					other.Status.Configuration.Operation.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{Targets: []cvk.NetworkControllerConfigurationGroupTarget{{PhysicalIdentity: "SECOND123"}}}
				}
				if err := c.Create(ctx, other); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(mode, "reservation") && mode != "late-reservation" && mode != "reservation-denied" {
				row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "retained", Namespace: "other-tenant"}, Spec: ops.ControllerConfigurationReservationSpec{PhysicalIdentity: "SECOND123"}}
				if mode == "settled-reservation" {
					at := metav1.Now()
					row.Status.ManagerSettledAt = &at
				}
				if mode == "deleting-reservation" {
					at := metav1.Now()
					row.DeletionTimestamp = &at
					row.Finalizers = []string{Finalizer}
				}
				if mode == "malformed-reservation" {
					row.Spec.PhysicalIdentity = " bad "
				}
				if err := c.Create(ctx, row); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "fence" {
				name, _ := BootstrapFenceName("second123")
				if err := c.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases"}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stale-controller" {
				nc = nc.DeepCopy()
				nc.ResourceVersion = "stale"
			}
			serials := []string{"bootstrap123", "second123"}
			namespace := "leases"
			if mode == "noncanonical" {
				serials[1] = "SECOND123"
			}
			if mode == "duplicate" {
				serials[1] = serials[0]
			}
			if mode == "empty" {
				serials = nil
			}
			if mode == "unknown-namespace" {
				namespace = ""
			}
			before := nc.DeepCopy()
			reader := &metadataScopeReader{Reader: c, mode: mode}
			err := CheckUnmanagedMetadataScope(ctx, reader, nc, namespace, serials)
			valid := mode == "valid" || mode == "unrelated"
			if (err == nil) != valid {
				t.Fatal("incorrect metadata scope result", err)
			}
			if !reflect.DeepEqual(before, nc) {
				t.Fatal("caller snapshot mutated")
			}
			var after cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Status.Configuration, after.Status.Configuration) {
				t.Fatal("read-only observation changed journal")
			}
			if valid && (reader.devices != 2 || reader.nodes != 2 || reader.controllers != 2 || reader.reservations != 2 || reader.leases != 4) {
				t.Fatal("did not perform two complete batch observations", reader)
			}
		})
	}
}
