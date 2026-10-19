// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newManagedFabricDevicesBulkFixture(t *testing.T, mode string) (*client, []fabricDeviceIntent, *fabricDevicesBulkFixture, ctrlclient.Client) {
	t.Helper()
	nativeMode := "create"
	if mode == "deferred-parent" {
		nativeMode = mode
	}
	if mode == "noop" {
		nativeMode = "noop"
	}
	if mode == "zone" {
		nativeMode = "zone"
	}
	c, wants, f := newFabricDevicesBulkFixture(t, nativeMode)
	objects := []ctrlclient.Object{}
	for i, inventory := range f.inventory {
		address := fmt.Sprintf("198.51.100.%d", 100+i)
		f.inventory[i].ManagementIP = address
		serial, err := topologyidentity.CanonicalPhysicalIdentity(inventory.Serial)
		if err != nil {
			t.Fatal(err)
		}
		name := inventory.Hostname
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: types.UID(name + "-device"), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: address, PhysicalIdentity: serial}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: name, NodeUID: name + "-node", PhysicalIdentity: serial}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-node")}}
		node.Status.NodeInfo.MachineID = serial
		if mode == "missing-incoming" && i == 1 || mode == "missing-peer" && i == 2 {
			objects = append(objects, node)
			continue
		}
		if i == 2 && mode == "peer-alias" {
			alias := device.DeepCopy()
			alias.Namespace, alias.Name, alias.UID = "other", "alias", "alias-uid"
			objects = append(objects, alias)
		}
		if i == 1 && mode == "wrong-address" {
			device.Spec.Address = "198.51.100.200"
		}
		if i == 1 && mode == "wrong-driver" {
			device.Spec.Driver = cvk.DeviceDriverNXOS
		}
		if i == 2 && mode == "node-replaced" {
			node.UID = "replaced-node"
		}
		objects = append(objects, device, node)
	}
	scheme := runtime.NewScheme()
	if err := ops.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cvk.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := core.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := configapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	api := fake.NewClientBuilder().WithScheme(scheme).WithIndex(&core.Pod{}, "spec.nodeName", func(obj ctrlclient.Object) []string { return []string{obj.(*core.Pod).Spec.NodeName} }).WithStatusSubresource(&cvk.NetworkController{}, &configapi.NetworkControllerConfig{}, &ops.ControllerConfigurationReservation{}).WithObjects(objects...).Build()
	return c, wants, f, api
}

func TestFabricDevicesBulkManagedFootprint(t *testing.T) {
	for _, mode := range []string{"valid", "zone", "missing-incoming", "missing-peer", "peer-alias", "wrong-address", "wrong-driver", "node-replaced", "guard-peer-node-race", "guard-incoming-address-race", "native-peer-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, wants, f, api := newManagedFabricDevicesBulkFixture(t, mode)
			calls := 0
			guard := func(context.Context, []Device) error {
				calls++
				if mode == "guard-incoming-address-race" {
					var device cvk.CiscoDevice
					if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "edge1"}, &device); err != nil {
						return err
					}
					device.Spec.Address = "198.51.100.200"
					return api.Update(ctx, &device)
				}
				if mode == "guard-peer-node-race" {
					var node core.Node
					if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "peer"}, &node); err != nil {
						return err
					}
					if err := api.Delete(ctx, &node); err != nil {
						return err
					}
					node.ResourceVersion = ""
					node.UID = "replacement"
					return api.Create(ctx, &node)
				}
				if mode == "native-peer-race" {
					f.inventory[2].Serial = "FOC99999999"
				}
				return nil
			}
			write, targets, err := c.planManagedFabricDevicesBulk(ctx, api, "lab", wants, guard)
			racing := mode == "guard-peer-node-race" || mode == "guard-incoming-address-race" || mode == "native-peer-race"
			valid := mode == "valid" || mode == "zone"
			if !valid && !racing {
				if err == nil || write != nil || targets != nil || calls != 0 || f.posts != 0 {
					t.Fatal("partial/aliased managed footprint exposed", err)
				}
				return
			}
			if err != nil || write == nil || len(targets) != 3 || len(write.identity.groupTargets) != 3 {
				t.Fatal("full managed footprint missing", err)
			}
			_, err = write.submit(ctx)
			if (err == nil) != valid || calls != 1 || valid && f.posts != 1 || !valid && f.posts != 0 {
				t.Fatal("managed dispatch observation", err)
			}
			if _, err := write.submit(ctx); err == nil {
				t.Fatal("managed closure replayed")
			}
		})
	}
}
