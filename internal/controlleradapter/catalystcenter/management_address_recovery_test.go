// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestManagementAddressRecoveryObservesWithoutSettling(t *testing.T) {
	for _, mode := range []string{"complete", "task-running", "task-failed", "native-pending", "endpoint-denied", "owner-lost", "source-changed", "lease-revoked", "proof-owner-race", "undeclared-reference"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			ref := configurationDeviceReference{Name: "switch", IP: "198.51.100.101"}
			before := &managementAddressChange{device: Device{ID: "native-id", Serial: "serial"}, before: "198.51.100.100", after: ref.IP}
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"switch","device_ip":"198.51.100.101","site":"Global/Lab","state":"ASSIGN"}]}}}`)}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, "inventory", managementAddressResource(ref, before, "serial"), "Update", configurationResourceIdentity{target: "native-id", serial: "serial"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			at := metav1.NewTime(now.Add(-5 * time.Second))
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.Operation.Phase, nc.Status.Configuration.Operation.TaskID, nc.Status.Configuration.Operation.SubmittedAt = "Verifying", "address-task", &at
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc, _ = a.currentController(ctx)
			op = nc.Status.Configuration.Operation.DeepCopy()
			name, _ := contract.ReservationName(string(nc.UID), op.ID)
			digest, _ := contract.OperationDigest(op)
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: "reservation", Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: config.Name, UID: string(config.UID)}, ControllerGeneration: 1, ConfigGeneration: 1, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device"}, Node: ops.ConfigurationReservationReference{Name: "switch", UID: "node"}, DeviceGeneration: 1, InventoryID: "native-id", PhysicalIdentity: "serial", ManagementAddress: before.before, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "worker"}, DeviceWorkerUsername: "system:serviceaccount:lab:network", DeviceWorkerRevision: "revision", ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "connector", UID: "connector"}, ControllerUsername: "system:serviceaccount:lab:connector", OperationID: op.ID, OperationDigest: digest}}
			holder, _ := contract.Holder("lab", r)
			leaseName := engine.LeaseName(devicecoordination.DeviceKey("lab", "switch"), devicecoordination.MutationLeaseFamily)
			leaseAt := metav1.NewMicroTime(now.Add(-30 * time.Second))
			renewed := metav1.NewMicroTime(now)
			duration, transitions := int32(120), int32(0)
			r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: leaseName, UID: "lease"}, Holder: holder, GrantedAt: metav1.NewTime(leaseAt.Time)}
			r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: digest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
			lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: leaseName, UID: "lease"}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, AcquireTime: &leaseAt, RenewTime: &renewed, LeaseDurationSeconds: &duration, LeaseTransitions: &transitions}}
			d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: before.before, PhysicalIdentity: "serial"}}
			d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeUID: "node", NodeName: "switch", PhysicalIdentity: "serial"}
			n := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node", Annotations: map[string]string{managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: "lab", managedprotocol.AnnotationDeviceName: "switch", managedprotocol.AnnotationDeviceUID: "device", managedprotocol.AnnotationNodeUID: "node", managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version, managedprotocol.AnnotationNetworkWorkerUsername: r.Spec.DeviceWorkerUsername}}}
			n.Status.NodeInfo.MachineID = "serial"
			if err := coordination.AddToScheme(a.statusWriter.Scheme()); err != nil {
				t.Fatal(err)
			}
			for _, object := range []ctrlclient.Object{r, lease, d, n} {
				if err := a.statusWriter.Create(ctx, object); err != nil {
					t.Fatal(err)
				}
			}
			installFabricGroupAuthorityFixture(t, a)
			a.configurationAdmission.protocol = contract.Protocol
			calls := 0
			a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if req.Method != http.MethodGet {
					t.Error("recovery submitted a mutation")
					w.WriteHeader(400)
					return
				}
				if req.URL.Path == taskPath+"address-task/tree" {
					task := map[string]any{"id": "address-task", "rootId": "address-task", "startTime": at.UnixMilli(), "endTime": now.UnixMilli(), "isError": mode == "task-failed"}
					if mode == "task-running" {
						delete(task, "endTime")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{task}})
					return
				}
				if req.URL.Path == devicesPath {
					address := ref.IP
					if mode == "native-pending" {
						address = before.before
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "native-id", Hostname: "switch", Serial: "SERIAL", ManagementIP: address, PlatformID: "C9300"}}})
					return
				}
				t.Error("unexpected recovery read")
			})
			nc, _ = a.currentController(ctx)
			original := nc.Status.Configuration.Operation.DeepCopy()
			switch mode {
			case "owner-lost":
				nc.Status.Configuration.Owners[0].ConfigUID = "other"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "source-changed":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "lease-revoked":
				var current ops.ControllerConfigurationReservation
				a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), &current)
				current.Status.Grant.Phase = "Revoked"
				if err := a.statusWriter.Status().Update(ctx, &current); err != nil {
					t.Fatal(err)
				}
			case "undeclared-reference":
				ref.Name = "not-in-source"
			}
			verified, err := a.observeManagementAddressRecovery(ctx, config, source, ref, func(context.Context, Device, string) error {
				calls++
				if mode == "endpoint-denied" {
					return errConfigurationDeviceAdmission
				}
				if mode == "proof-owner-race" {
					current, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					current.Status.Configuration.Owners[0].ConfigUID = "changed-during-proof"
					return a.statusWriter.Status().Update(ctx, current)
				}
				return nil
			})
			if verified != (mode == "complete") || (err != nil) != (mode != "complete" && mode != "task-running" && mode != "native-pending") {
				t.Fatalf("mode=%s verified=%v error=%v", mode, verified, err)
			}
			if mode != "complete" && mode != "endpoint-denied" && mode != "proof-owner-race" && calls != 0 {
				t.Fatal("endpoint proof requested after earlier evidence failed")
			}
			if !reflect.DeepEqual(journalState(t, a).Operation, original) {
				t.Fatal("native recovery advanced or cleared the journal")
			}
		})
	}
}
