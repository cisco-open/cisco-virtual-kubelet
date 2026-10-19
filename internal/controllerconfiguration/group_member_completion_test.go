// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReadGroupMemberCompletion(t *testing.T) {
	for _, mode := range []string{"valid", "missing-peer", "missing-peer-completion", "foreign-task", "missing-receipt", "stale-member", "changed-member"} {
		t.Run(mode, func(t *testing.T) {
			nc, rows, now := groupCompletionFixture(t)
			if mode == "missing-peer-completion" {
				rows[1].Status.Completion = nil
			}
			if mode == "foreign-task" {
				rows[1].Status.Completion.TaskID = "foreign"
			}
			if mode == "missing-receipt" {
				nc.Status.Configuration.ActionApplications = nil
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			objects := []client.Object{nc, rows[0]}
			if mode != "missing-peer" {
				objects = append(objects, rows[1])
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			ctx := context.Background()
			if err := c.Get(ctx, client.ObjectKeyFromObject(rows[0]), rows[0]); err != nil {
				t.Fatal(err)
			}
			if mode == "stale-member" {
				rows[0].ResourceVersion = "stale"
			}
			if mode == "changed-member" {
				rows[0].Spec.InventoryID = "foreign"
			}
			if err := ReadGroupMemberCompletion(ctx, c, rows[0], now); (err == nil) != (mode == "valid") {
				t.Fatal("complete receipt read", err)
			}
		})
	}
}
