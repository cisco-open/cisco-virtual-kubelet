// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestManagementAddressManagedTargetsPreserveOldEndpointAuthority(t *testing.T) {
	for _, mode := range []string{"change", "no-op", "managed-submit", "guard-denied", "declaration-race", "node-race", "premature-address-change", "missing-device", "missing-node", "replaced-node", "alias", "wrong-driver", "native-change"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			ref := configurationDeviceReference{Name: "switch", IP: "198.51.100.101"}
			if mode == "no-op" {
				ref.IP = "198.51.100.100"
			}
			native := Device{ID: "native-switch", Hostname: "switch", Serial: "SERIAL", ManagementIP: "198.51.100.100", PlatformID: "C9300"}
			reads, writes := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == http.MethodPut && r.URL.Path == devicesPath+"/"+native.ID+"/management-address" {
					writes++
					native.ManagementIP = ref.IP
					fmt.Fprint(w, `{"response":{"taskId":"managed-task"}}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != devicesPath {
					t.Error("preflight requested a mutation")
					return
				}
				reads++
				current := native
				if mode == "native-change" && reads > 1 {
					current.ManagementIP = "198.51.100.103"
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []Device{current}})
			})
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: native.ManagementIP, PhysicalIdentity: "serial"}}
			device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeName: "switch", NodeUID: "node", PhysicalIdentity: "serial"}
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node"}}
			node.Status.NodeInfo.MachineID = "serial"
			if mode == "premature-address-change" {
				device.Spec.Address = ref.IP
			}
			if mode == "replaced-node" {
				node.UID = "replacement"
			}
			if mode == "wrong-driver" {
				device.Spec.Driver = cvk.DeviceDriverNXOS
			}
			objects := []ctrlclient.Object{}
			if mode != "missing-device" {
				objects = append(objects, device)
			}
			if mode != "missing-node" {
				objects = append(objects, node)
			}
			if mode == "alias" {
				alias := device.DeepCopy()
				alias.Namespace, alias.Name, alias.UID = "other", "alias", "alias"
				objects = append(objects, alias)
			}
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			snapshot, targets, err := c.managementAddressTargets(ctx, reader, "lab", ref)
			valid := mode == "change" || mode == "no-op" || mode == "managed-submit" || mode == "guard-denied" || mode == "declaration-race" || mode == "node-race"
			if !valid {
				if err == nil || snapshot != nil || targets != nil {
					t.Fatal("unsafe managed preflight returned partial evidence")
				}
				return
			}
			if err != nil || snapshot == nil || len(targets) != 1 || targets[0].InventoryID != native.ID || targets[0].PhysicalIdentity != "serial" || targets[0].Device.UID != "device" || targets[0].Node.UID != "node" {
				t.Fatalf("managed identity lost: snapshot=%+v targets=%+v error=%v", snapshot, targets, err)
			}
			if (snapshot.change != nil) != (mode != "no-op") || snapshot.native.ManagementIP != native.ManagementIP || snapshot.requested != ref.IP {
				t.Fatal("requested address replaced old authoritative endpoint")
			}
			var unchanged cvk.CiscoDevice
			if err := reader.Get(ctx, ctrlclient.ObjectKeyFromObject(device), &unchanged); err != nil || unchanged.Spec.Address != native.ManagementIP {
				t.Fatal("preflight modified device declaration", err)
			}
			guards := 0
			write, plannedTargets, err := c.planManagedManagementAddressChange(ctx, reader, "lab", ref, func(ctx context.Context, _ Device, _ string) error {
				guards++
				switch mode {
				case "guard-denied":
					return errConfigurationDeviceAdmission
				case "declaration-race":
					device.Spec.Address = ref.IP
					return reader.Update(ctx, device)
				case "node-race":
					if err := reader.Delete(ctx, node); err != nil {
						return err
					}
					node.UID, node.ResourceVersion = "replacement", ""
					return reader.Create(ctx, node)
				}
				return nil
			})
			if err != nil || len(plannedTargets) != 1 {
				t.Fatal("managed staging failed", err)
			}
			if mode == "no-op" {
				if write != nil || guards != 0 || writes != 0 {
					t.Fatal("no-op acquired authority or submitted")
				}
				return
			}
			if write == nil {
				t.Fatal("missing managed write")
			}
			if mode == "change" {
				return
			}
			task, err := write.submit(ctx)
			if guards != 1 || (err == nil) != (mode == "managed-submit") || (writes == 1) != (mode == "managed-submit") || (mode == "managed-submit" && task.ID != "managed-task") {
				t.Fatalf("guards=%d writes=%d task=%v error=%v", guards, writes, task, err)
			}
			if _, err := write.submit(ctx); err == nil {
				t.Fatal("managed submission closure replayed")
			}
		})
	}
}
