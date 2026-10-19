// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package maintenance

import (
	"context"
	"errors"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigurationPreparationRequiresBothInventories(t *testing.T) {
	for _, scenario := range []string{"empty", "retained terminal", "other node", "pending", "terminating terminal", "device application", "unknown device entry", "scan failure", "nil scanner", "guard removed during scan", "pod appears during scan", "claimed during scan", "pod list failure", "cancelled during scan"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, device, node, _, lease := managedCoordinatorFixture(t)
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "configuration", Namespace: device.Namespace, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Device: ops.ConfigurationReservationReference{Name: device.Name, UID: string(device.UID)}, DeviceGeneration: device.Generation, Node: ops.ConfigurationReservationReference{Name: node.Name, UID: string(node.UID)}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: c.WorkerPodName, UID: c.WorkerPodUID}, DeviceWorkerUsername: c.ExpectedWorkerUsername, ConfigGeneration: 1}}
			holder, _ := contract.Holder(r.Namespace, r)
			lease.Spec.HolderIdentity = &holder
			// Model the field selector supported by the uncached API reader.
			c.Client = fake.NewClientBuilder().WithScheme(c.Client.Scheme()).WithStatusSubresource(device, r).WithObjects(device, node, lease, r).WithIndex(&core.Pod{}, "spec.nodeName", func(o client.Object) []string { return []string{o.(*core.Pod).Spec.NodeName} }).Build()
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			request, err := c.publishConfigurationMaintenanceRequest(ctx, r, node)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
				t.Fatal(err)
			}
			device.Status.MaintenanceSession = acknowledgement(request, c, node)
			device.Status.MaintenanceSession.ProtocolVersion = request.ProtocolVersion
			device.Status.MaintenanceSession.Purpose = request.Purpose
			if err := c.Client.Status().Update(ctx, device); err != nil {
				t.Fatal(err)
			}
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(node), node); err != nil {
				t.Fatal(err)
			}
			node.Spec.Taints = []core.Taint{{Key: TaintKey, Value: TaintValue, Effect: core.TaintEffectNoSchedule}}
			if err := c.Client.Update(ctx, node); err != nil {
				t.Fatal(err)
			}
			if scenario == "pod list failure" {
				c.Client = failedConfigurationPodList{Client: c.Client}
			}
			pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload", Namespace: "other-namespace", UID: "pod-uid"}, Spec: core.PodSpec{NodeName: node.Name}, Status: core.PodStatus{Phase: core.PodPending}}
			switch scenario {
			case "retained terminal", "terminating terminal":
				pod.Status.Phase = core.PodSucceeded
			case "other node":
				pod.Spec.NodeName = "other"
			}
			if scenario == "terminating terminal" {
				v := metav1.Now()
				pod.DeletionTimestamp = &v
				pod.Finalizers = []string{"test/retained"}
			}
			if scenario == "pending" || scenario == "retained terminal" || scenario == "terminating terminal" || scenario == "other node" {
				if err := c.Client.Create(ctx, pod); err != nil {
					t.Fatal(err)
				}
				if scenario == "terminating terminal" {
					if err := c.Client.Delete(ctx, pod); err != nil {
						t.Fatal(err)
					}
				}
			}
			scans := 0
			var scan func(context.Context) ([]*core.Pod, error) = func(context.Context) ([]*core.Pod, error) {
				scans++
				switch scenario {
				case "device application":
					return []*core.Pod{{Status: core.PodStatus{Phase: core.PodSucceeded}}}, nil
				case "unknown device entry":
					return []*core.Pod{nil}, nil
				case "scan failure":
					return nil, errors.New("incomplete native inventory")
				case "guard removed during scan":
					node.Spec.Taints = nil
					if err := c.Client.Update(ctx, node); err != nil {
						t.Fatal(err)
					}
				case "pod appears during scan":
					if err := c.Client.Create(ctx, pod); err != nil {
						t.Fatal(err)
					}
				case "claimed during scan":
					changed := r.DeepCopy()
					changed.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: "concurrent"}
					if err := c.Client.Status().Update(ctx, changed); err != nil {
						t.Fatal(err)
					}
				case "cancelled during scan":
					cancel()
				}
				return nil, nil
			}
			if scenario == "nil scanner" {
				scan = nil
			}
			err = c.PrepareConfigurationWorkloads(ctx, r, scan)
			wantSuccess := scenario == "empty" || scenario == "retained terminal" || scenario == "other node"
			if (err == nil) != wantSuccess {
				t.Fatalf("preparation error = %v; want success %v", err, wantSuccess)
			}
			if (scenario == "pending" || scenario == "terminating terminal" || scenario == "nil scanner" || scenario == "pod list failure") && scans != 0 {
				t.Fatal("scanned despite blocked preparation")
			}
			if wantSuccess && scans != 1 {
				t.Fatal("preparation did not use strict inventory")
			}
			var current ops.ControllerConfigurationReservation
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(r), &current); err != nil {
				t.Fatal(err)
			}
			if current.Status.Grant != nil || (scenario != "claimed during scan" && current.Status.Claim != nil) {
				t.Fatal("inventory check authorized a mutation")
			}
		})
	}
}

type failedConfigurationPodList struct{ client.Client }

func (c failedConfigurationPodList) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*core.PodList); ok {
		return errors.New("Pod API unavailable")
	}
	return c.Client.List(ctx, list, opts...)
}
