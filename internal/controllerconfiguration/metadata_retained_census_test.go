// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type retainedMetadataCensusReader struct {
	client.Reader
	other       *cvk.NetworkController
	late        bool
	controllers int
}

func (r *retainedMetadataCensusReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := &client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(o)
	}
	if o.Namespace != "" || o.LabelSelector != nil || o.FieldSelector != nil {
		return errors.New("retained metadata requires complete cluster census")
	}
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	if rows, ok := list.(*cvk.NetworkControllerList); ok {
		r.controllers++
		if !r.late || r.controllers == 2 {
			rows.Items = append(rows.Items, *r.other.DeepCopy())
		}
	}
	return nil
}

func TestRetainedMetadataHandoffIdentityCensus(t *testing.T) {
	for _, kind := range []string{"metadata", "bootstrap"} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"retained", "active-operation", "refused", "released", "deleting", "late", "unrelated", "other-provider", "empty", "malformed", "noncanonical", "duplicate", "unknown-protocol"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					c, nc, _ := bootstrapIdentityFixture(t)
					_ = ops.AddToScheme(c.Scheme())
					_ = coordination.AddToScheme(c.Scheme())
					if kind == "metadata" {
						nc.Status.Configuration.Operation.Action = "Delete"
						nc.Status.Configuration.Operation.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
						nc.Status.Configuration.Operation.TargetSerial = ""
						if err := c.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
							t.Fatal(err)
						}
					}
					original := nc.DeepCopy()
					other := nc.DeepCopy()
					other.Namespace = "other-tenant"
					other.UID = "other-controller"
					other.ResourceVersion = "other-version"
					prepared := other.Status.Configuration.Operation.DeepCopy()
					prepared.Action = "Delete"
					prepared.TargetSerial = ""
					prepared.Resource = "pnp-remove:v2:retained-pnp:" + strings.Repeat("b", 64)
					h := &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: strings.Repeat("c", 64), LeaseNamespace: "leases", WorkerPod: ops.ConfigurationReservationReference{Name: "old-worker", UID: "old-worker-uid"}, WorkerUsername: "system:serviceaccount:other-tenant:connector", PhysicalIdentities: []string{"bootstrap123"}, Operation: *prepared}
					other.Status.Configuration.MetadataHandoff = h
					other.Status.Configuration.Operation = nil
					switch mode {
					case "active-operation":
						other.Status.Configuration.Operation = prepared.DeepCopy()
					case "refused":
						now := metav1.Now()
						h.RefusedAt = &now
						h.RefusedSubmissionAt = &now
					case "released":
						now := metav1.Now()
						h.ReleasedAt = &now
					case "deleting":
						now := metav1.Now()
						other.DeletionTimestamp = &now
						other.Finalizers = []string{"retain-test-custody"}
					case "unrelated":
						h.PhysicalIdentities = []string{"different123"}
					case "other-provider":
						other.Spec.Type = "nexus-dashboard"
					case "empty":
						h.PhysicalIdentities = nil
					case "malformed":
						h.PhysicalIdentities = []string{"bad serial"}
					case "noncanonical":
						h.PhysicalIdentities = []string{"BOOTSTRAP123"}
					case "duplicate":
						h.PhysicalIdentities = append(h.PhysicalIdentities, h.PhysicalIdentities[0])
					case "unknown-protocol":
						h.Protocol = "future-unrecognized-protocol"
					}
					before := other.DeepCopy()
					reader := &retainedMetadataCensusReader{Reader: c, other: other, late: mode == "late"}
					var err error
					if kind == "metadata" {
						err = CheckUnmanagedMetadataScope(ctx, reader, nc, "leases", []string{"bootstrap123", "second123"})
					} else {
						_, err = ReadBootstrapIdentity(ctx, reader, nc)
					}
					if (err == nil) != (mode == "unrelated") {
						t.Fatal("retained custody was lost or unrelated cohort blocked", err)
					}
					if !reflect.DeepEqual(before, other) || !reflect.DeepEqual(original, nc) {
						t.Fatal("read-only census changed caller custody")
					}
					if mode == "late" && reader.controllers != 2 {
						t.Fatal("late handoff was not found on repeated global census")
					}
					var current cvk.NetworkController
					if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(original.Status.Configuration, current.Status.Configuration) {
						t.Fatal("census changed original journal")
					}
				})
			}
		})
	}
}
