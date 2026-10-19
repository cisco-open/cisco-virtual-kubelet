// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPhysicalVerificationRetainsAtomicActionReceipt(t *testing.T) {
	for _, scenario := range []string{"verified", "missing task", "old receipt retained", "old receipt retired", "foreign owner", "pruned receipt"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			identity := configurationResourceIdentity{target: "device", serial: "SERIAL"}
			op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device-action", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "task"); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing task" {
				nc.Status.Configuration.Operation.TaskID = ""
			}
			if scenario == "old receipt retained" || scenario == "old receipt retired" || scenario == "foreign owner" {
				old := nc.Status.Configuration.Operation.DeepCopy()
				old.ID, old.Phase = strings.Repeat("b", 32), "Verified"
				if scenario == "foreign owner" {
					old.ConfigUID = "other-owner"
				}
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: old.Section, Resource: old.Resource, Operation: *old}}
				if scenario == "old receipt retained" {
					name, _ := contract.ReservationName(string(nc.UID), old.ID)
					r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: nc.Namespace}}
					if err := a.statusWriter.Create(ctx, r); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			before := nc.Status.Configuration.DeepCopy()
			if scenario == "pruned receipt" {
				a.statusWriter = pruningActionReceiptClient{Client: a.statusWriter}
			}
			err = a.advanceConfiguration(ctx, op.ID, "Verified", "")
			state := journalState(t, a)
			if scenario == "verified" || scenario == "old receipt retired" {
				if err != nil || state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || !reflect.DeepEqual(state.ActionApplications[0].Operation, *state.Operation) {
					t.Fatal("receipt not committed with verification", err)
				}
				if state.ActionApplications[0].VerifiedAt == nil {
					t.Fatal("verification timestamp missing")
				}
				firstVerified := state.ActionApplications[0].VerifiedAt.DeepCopy()
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.retainPhysicalActionReceipt(ctx, current); err != nil {
					t.Fatal(err)
				}
				if !current.Status.Configuration.ActionApplications[0].VerifiedAt.Equal(firstVerified) {
					t.Fatal("receipt retry reset dependency pacing")
				}
			} else if scenario == "pruned receipt" {
				if err == nil || state.Operation.Phase != "Verified" || len(state.ActionApplications) != 0 {
					t.Fatal("pruned receipt reported persisted", err)
				}
			} else if err == nil || !reflect.DeepEqual(state, before) {
				t.Fatal("failed receipt changed journal", err)
			}
		})
	}
}

type pruningActionReceiptClient struct{ ctrlclient.Client }

func (c pruningActionReceiptClient) Status() ctrlclient.SubResourceWriter {
	return pruningActionReceiptWriter{SubResourceWriter: c.Client.Status()}
}

type pruningActionReceiptWriter struct{ ctrlclient.SubResourceWriter }

func (w pruningActionReceiptWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil {
		nc.Status.Configuration.ActionApplications = nil
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
