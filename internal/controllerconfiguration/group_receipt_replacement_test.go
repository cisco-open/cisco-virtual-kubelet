// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestPersistReplacingVerifiedGroupReceipt(t *testing.T) {
	for _, mode := range []string{"retired", "default-refuses", "retained", "released", "unexpected-name", "late-reservation", "foreign-owner", "bad-prior-time", "changed-predecessor", "missing-predecessor"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			op := nc.Status.Configuration.Operation
			op.Phase = "Verifying"
			op.TaskID = "task"
			submitted := metav1.NewTime(now.Add(-9 * time.Second))
			op.SubmittedAt = &submitted
			nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: op.Section, ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}
			at := metav1.NewTime(now.Add(-time.Second))
			next := &cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: "native:binding", Operation: *op.DeepCopy(), VerifiedAt: &at}
			next.Operation.Phase = "Verified"
			old := next.DeepCopy()
			old.Operation.ID = strings.Repeat("b", 32)
			old.Operation.TaskID = "old-task"
			oldAt := metav1.NewTime(now.Add(-20 * time.Second))
			oldSubmitted := metav1.NewTime(now.Add(-30 * time.Second))
			old.VerifiedAt = &oldAt
			old.Operation.SubmittedAt = &oldSubmitted
			if mode == "foreign-owner" {
				old.Operation.ConfigUID = "foreign"
			}
			if mode == "bad-prior-time" {
				old.VerifiedAt = &metav1.Time{Time: now.Add(time.Hour)}
			}
			var err error
			old.Operation.PhysicalGroup.Digest, err = GroupOperationDigest(&old.Operation, old.Operation.PhysicalGroup.Targets)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{*old.DeepCopy()}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			for _, r := range members {
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			retained := members[0].DeepCopy()
			retained.ResourceVersion = ""
			retained.UID = "old-member"
			retained.Name = groupMemberReservationName(string(nc.UID), old.Operation.ID, old.Operation.PhysicalGroup.Digest, old.Operation.PhysicalGroup.Targets[0].PhysicalIdentity)
			retained.Spec.OperationID = old.Operation.ID
			if mode == "unexpected-name" {
				retained.Name = "unexpected-old-member"
			}
			if mode == "released" {
				retained.Status.Grant.Phase = "Released"
			}
			if mode == "retained" || mode == "released" || mode == "unexpected-name" {
				if err := c.Create(ctx, retained); err != nil {
					t.Fatal(err)
				}
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "late-reservation" {
					return c.Create(ctx, retained)
				}
				return nil
			}
			before := nc.Status.Configuration.DeepCopy()
			previous := old.DeepCopy()
			if mode == "changed-predecessor" {
				previous.Operation.TaskID = "different"
			}
			if mode == "missing-predecessor" {
				previous = nil
			}
			if mode == "default-refuses" {
				err = PersistVerifiedGroupReceipt(ctx, c, c, nc, members, next, authorize, now)
			} else {
				err = PersistReplacingVerifiedGroupReceipt(ctx, c, c, nc, members, next, authorize, now, previous)
			}
			if (err == nil) != (mode == "retired") {
				t.Fatal("replacement result", err)
			}
			var actual cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &actual); err != nil {
				t.Fatal(err)
			}
			if mode == "retired" {
				if actual.Status.Configuration.Operation.Phase != "Verified" || len(actual.Status.Configuration.ActionApplications) != 1 || !equality.Semantic.DeepEqual(actual.Status.Configuration.ActionApplications[0], *next) {
					t.Fatal("replacement not atomic")
				}
			} else if !equality.Semantic.DeepEqual(actual.Status.Configuration, before) {
				t.Fatal("refused replacement changed status")
			}
		})
	}
}
