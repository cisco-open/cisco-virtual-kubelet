// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"reflect"
	"testing"

	coordv1 "k8s.io/api/coordination/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestManagedLeaseRebindPreservesActiveRequest(t *testing.T) {
	r, device, node, _, lease := managerMaintenanceFixture(t)
	ctx := context.Background()
	if err := r.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	before := lease.DeepCopy()
	node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = "replacement-pod"
	if err := r.ensureManagedMutationLease(ctx, device, node); err != nil {
		t.Fatal(err)
	}
	var current coordv1.Lease
	if err := r.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Spec, current.Spec) || before.UID != current.UID {
		t.Fatal("binding repair changed the retained lock")
	}
	for key, value := range before.Annotations {
		if key != managedprotocol.AnnotationNetworkWorkerPodUID && current.Annotations[key] != value {
			t.Fatalf("binding repair changed annotation %s", key)
		}
	}
	if current.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] != "replacement-pod" {
		t.Fatal("worker Pod binding was not repaired")
	}
	// Ownership collisions are not repairable, even when the name is canonical.
	current.Annotations[managedprotocol.AnnotationDeviceUID] = "foreign-device"
	if err := r.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedMutationLease(ctx, device, node); err == nil {
		t.Fatal("foreign device ownership was silently adopted")
	}
}
