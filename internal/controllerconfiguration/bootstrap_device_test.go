// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type unknownEnrollmentReader struct {
	client.Reader
	mode           string
	lists, devices int
}

func (r *unknownEnrollmentReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := &client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(o)
	}
	if o.Namespace != "leases" || o.LabelSelector != nil || o.FieldSelector != nil || o.Limit != 0 {
		return errors.New("unknown enrollment requires complete shared fence namespace")
	}
	r.lists++
	if r.mode == "denied" {
		return errors.New("lease list forbidden")
	}
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	rows := list.(*coordination.LeaseList)
	if r.mode == "late-fence" && r.lists == 2 {
		rows.Items = append(rows.Items, coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: bootstrapFenceNamePrefix + "late", Namespace: "leases"}})
	}
	if r.mode == "truncated" {
		rows.Continue = "next"
	}
	if r.mode == "remaining" {
		one := int64(1)
		rows.RemainingItemCount = &one
	}
	return nil
}
func (r *unknownEnrollmentReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if d, ok := obj.(*cvk.CiscoDevice); ok {
		r.devices++
		if r.mode == "late-device" && r.devices == 2 {
			d.ResourceVersion = "changed"
		}
	}
	return nil
}

func TestUnknownBootstrapDeviceEnrollment(t *testing.T) {
	for _, mode := range []string{"absent", "held", "metadata-released", "malformed-key", "other-namespace", "unrelated-lease", "late-fence", "denied", "truncated", "remaining", "late-device", "known-disjoint", "known-collision"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = coordination.AddToScheme(scheme)
			d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "tenant", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Address: "198.51.100.101", Driver: "iosxe"}}
			if mode == "known-disjoint" {
				d.Spec.PhysicalIdentity = "other123"
			}
			if mode == "known-collision" {
				d.Spec.PhysicalIdentity = "serial123"
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(d).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
				t.Fatal(err)
			}
			if mode == "held" || mode == "metadata-released" || mode == "malformed-key" || mode == "other-namespace" || mode == "unrelated-lease" || mode == "known-disjoint" || mode == "known-collision" {
				name, _ := BootstrapFenceName("serial123")
				l := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases"}}
				if mode == "metadata-released" {
					l.Labels = map[string]string{"ops.cisco.vk/metadata-fence": "cvk-metadata-fence.v1"}
					l.Annotations = map[string]string{"ops.cisco.vk/metadata-fence-release": "old-settlement"}
				}
				if mode == "malformed-key" {
					l.Name = bootstrapFenceNamePrefix + "malformed"
				}
				if mode == "other-namespace" {
					l.Namespace = "tenant"
				}
				if mode == "unrelated-lease" {
					l.Name = "leader-election"
				}
				if err := c.Create(ctx, l); err != nil {
					t.Fatal(err)
				}
			}
			reader := &unknownEnrollmentReader{Reader: c, mode: mode}
			err := CheckBootstrapDeviceEnrollment(ctx, reader, "leases", d)
			allowed := mode == "absent" || mode == "other-namespace" || mode == "unrelated-lease" || mode == "known-disjoint"
			if (err == nil) != allowed {
				t.Fatal("unexpected unknown-identity enrollment result", err)
			}
			if allowed && mode != "known-disjoint" && (reader.lists != 2 || reader.devices != 2) {
				t.Fatal("unknown enrollment omitted repeated namespace/current-source reads")
			}
			if mode == "known-disjoint" && reader.lists != 0 {
				t.Fatal("known identity performed broad unknown-alias census")
			}
		})
	}
}
