// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type taskReceiptWriter struct {
	client.Client
	mode string
}
type taskReceiptStatus struct {
	client.SubResourceWriter
	mode string
}

func (w taskReceiptWriter) Status() client.SubResourceWriter {
	return taskReceiptStatus{w.Client.Status(), w.mode}
}
func (w taskReceiptStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Accepted" {
		copy := nc.DeepCopy()
		if w.mode == "pruned" {
			copy.Status.Configuration.Operation.TaskID = ""
		}
		if err := w.SubResourceWriter.Update(ctx, copy, opts...); err != nil {
			return err
		}
		if w.mode == "lost" {
			return errors.New("lost status response")
		}
		return nil
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestGroupDispatchTaskReceipt(t *testing.T) {
	for _, mode := range []string{"valid", "before-check", "denied-check", "denied-receipt", "changed-journal", "revoked-grant", "expired-grant", "paused-controller", "invalid-task", "pruned", "lost"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			deny := false
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if deny {
					return errors.New("denied")
				}
				return nil
			}
			writer := taskReceiptWriter{Client: c, mode: mode}
			permit, err := ClaimGroupForController(ctx, c, writer, nc, members, "leases", authorize, now)
			if err != nil {
				t.Fatal(err)
			}
			dispatch, err := permit.BeginSubmission(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "denied-check" {
				deny = true
			}
			if mode != "before-check" {
				err := dispatch.Check(ctx, now)
				if (err != nil) != (mode == "denied-check") {
					t.Fatal("dispatch check", err)
				}
			}
			if mode == "denied-receipt" {
				deny = true
			}
			if mode == "changed-journal" {
				var fresh cvk.NetworkController
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &fresh); err != nil {
					t.Fatal(err)
				}
				fresh.Status.Configuration.Operation.Resource = "changed"
				if err := c.Status().Update(ctx, &fresh); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "revoked-grant" {
				var member ops.ControllerConfigurationReservation
				if err := c.Get(ctx, client.ObjectKeyFromObject(members[0]), &member); err != nil {
					t.Fatal(err)
				}
				member.Status.Grant.Phase = "Revoked"
				if err := c.Status().Update(ctx, &member); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "paused-controller" {
				var fresh cvk.NetworkController
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &fresh); err != nil {
					t.Fatal(err)
				}
				fresh.Spec.Paused = true
				if err := c.Update(ctx, &fresh); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "expired-grant" {
				now = now.Add(24 * time.Hour)
			}
			task := "task-id"
			if mode == "invalid-task" {
				task = "bad/id"
			}
			err = dispatch.RecordTask(ctx, task, now)
			if (err == nil) != (mode == "valid") {
				t.Fatal("receipt result", err)
			}
			var stored cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &stored); err != nil {
				t.Fatal(err)
			}
			op := stored.Status.Configuration.Operation
			accepted := mode == "valid" || mode == "lost" || mode == "pruned"
			if (op.Phase == "Accepted") != accepted {
				t.Fatal("unexpected phase", op.Phase)
			}
			if accepted && mode != "pruned" && op.TaskID != "task-id" {
				t.Fatal("missing durable task")
			}
			alias := *dispatch
			if err := alias.RecordTask(ctx, task, now); err == nil {
				t.Fatal("copied receipt permit reused")
			}
			if err := alias.Check(ctx, now); err == nil && mode != "before-check" {
				t.Fatal("receipt restored dispatch authority")
			}
		})
	}
}
