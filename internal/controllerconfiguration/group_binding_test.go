// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGroupReservationSetBinding(t *testing.T) {
	for _, mode := range []string{"valid", "phase", "missing", "duplicate", "uid", "name", "protocol", "digest", "inventory", "serial", "node", "device", "config", "controller", "connector", "deleting", "finalizer", "owner"} {
		t.Run(mode, func(t *testing.T) {
			nc, members, connector, now := groupCandidateFixture(t)
			reservations, err := GroupReservationCandidates(nc, members, connector, now)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range reservations {
				r.UID = types.UID(r.Name + "-uid")
			}
			reservations[0], reservations[1] = reservations[1], reservations[0]
			r := reservations[1]
			switch mode {
			case "phase":
				nc.Status.Configuration.Operation.Phase = "Unknown"
			case "missing":
				reservations = reservations[:1]
			case "duplicate":
				reservations[1] = reservations[0]
			case "uid":
				r.UID = ""
			case "name":
				r.Name = "other"
			case "protocol":
				r.Spec.Protocol = Protocol
			case "digest":
				r.Spec.OperationDigest = "other"
			case "inventory":
				r.Spec.InventoryID = "other"
			case "serial":
				r.Spec.PhysicalIdentity = "other"
			case "node":
				r.Spec.Node.UID = "other"
			case "device":
				r.Spec.Device.UID = "other"
			case "config":
				r.Spec.ConfigGeneration++
			case "controller":
				r.Spec.Controller.UID = "other"
			case "connector":
				r.Spec.ControllerWorkerPod.UID = "other"
			case "deleting":
				at := metav1.NewTime(now)
				r.DeletionTimestamp = &at
			case "finalizer":
				r.Finalizers = nil
			case "owner":
				r.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}}
			}
			bound, err := ValidateGroupReservationSet(nc, reservations)
			if mode != "valid" && mode != "phase" {
				if err == nil || bound != nil {
					t.Fatal("partial or mixed group accepted", err)
				}
				return
			}
			if err != nil || len(bound) != 2 || bound[0].Spec.PhysicalIdentity != "serial-a" {
				t.Fatal("valid group refused", err)
			}
			bound[0].Spec.InventoryID = "mutated"
			if reservations[1].Spec.InventoryID == "mutated" {
				t.Fatal("validation returned aliased input")
			}
		})
	}
}
