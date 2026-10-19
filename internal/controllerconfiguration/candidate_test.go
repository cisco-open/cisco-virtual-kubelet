// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
)

func TestReservationCandidate(t *testing.T) {
	for _, scenario := range []string{"valid", "submitted", "receipt present", "wrong physical target", "wrong controller generation", "unready connector", "replacement device worker", "foreign namespace", "paused", "credential operation", "dependency envelope", "dependency graph"} {
		t.Run(scenario, func(t *testing.T) {
			_, d, n, p, now := deviceBindingFixture(t)
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "intent", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "wireless-provision", Action: "Update", Phase: "Prepared", TargetID: "inventory-id", TargetSerial: "SERIAL123"}
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: d.Namespace, Name: "cc", UID: "controller-uid", Generation: 1}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op}}}
			cp := &core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: d.Namespace, Name: "connector", UID: "connector-uid"}, Spec: core.PodSpec{ServiceAccountName: "connector"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(now)}}}}
			switch scenario {
			case "submitted":
				op.Phase = "Submitting"
			case "receipt present":
				op.TaskID = "task"
			case "wrong physical target":
				op.TargetSerial = "other"
			case "wrong controller generation":
				nc.Generation++
			case "unready connector":
				cp.Status.Conditions = nil
			case "replacement device worker":
				p.UID = "replacement"
			case "foreign namespace":
				cp.Namespace = "other"
			case "paused":
				nc.Spec.Paused = true
			case "dependency graph":
				op.Resource = "template-composite-deploy:" + strings.Repeat("c", 64)
				op.ParentID = "site"
				op.DependencyVersions = dependencyGraphFixture()
			case "dependency envelope":
				op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1"}
			case "credential operation":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{}}
			}
			before := nc.DeepCopy()
			r, err := ReservationCandidate(nc, d, n, p, cp, now)
			if scenario != "valid" && scenario != "dependency graph" {
				if err == nil {
					t.Fatal("invalid candidate accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.UID != "" || r.ResourceVersion != "" || len(r.OwnerReferences) != 0 || !reflect.DeepEqual(r.Finalizers, []string{Finalizer}) || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.Completion != nil {
				t.Fatal("candidate acquired runtime identity or authority")
			}
			if r.Spec.PhysicalIdentity != "serial123" || r.Spec.InventoryID != op.TargetID || r.Spec.DeviceWorkerPod.UID != string(p.UID) || r.Spec.ControllerWorkerPod.UID != string(cp.UID) {
				t.Fatal("lost target/worker binding")
			}
			if ValidateDeviceWorkerBinding(r, d, n, p, now) == nil {
				t.Fatal("pending candidate passed runtime UID-bound gate")
			}
			again, err := ReservationCandidate(nc, d, n, p, cp, now)
			if err != nil || !reflect.DeepEqual(r, again) {
				t.Fatal("candidate unstable")
			}
			if !reflect.DeepEqual(nc, before) {
				t.Fatal("candidate mutated source journal")
			}
			r.UID = "persisted-reservation-uid"
			if err := ValidateOperationBinding(r, op); err != nil {
				t.Fatal("candidate does not bind prepared operation", err)
			}
			if err := ValidateDeviceWorkerBinding(r, d, n, p, now); err != nil {
				t.Fatal(err)
			}
			if scenario == "dependency graph" {
				op.DependencyVersions.Entries[1].Version++
				if err := ValidateOperationBinding(r, op); err == nil {
					t.Fatal("changed graph retained reservation binding")
				}
			}

		})
	}
}
