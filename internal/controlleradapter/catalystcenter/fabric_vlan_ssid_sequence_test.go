// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func TestFabricVLANSSIDSequentialAddAndMove(t *testing.T) {
	testFabricVLANSSIDSequence(t, false, false, false)
}
func TestFabricVLANSSIDPublicSequentialAddAndMove(t *testing.T) {
	testFabricVLANSSIDSequence(t, true, false, false)
}
func TestFabricVLANSSIDPublicMixedMulticast(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained-predecessor-%t", retained), func(t *testing.T) {
			testFabricVLANSSIDSequence(t, true, true, retained)
		})
	}
}
func TestFabricVLANSSIDPublicEmbeddedMulticast(t *testing.T) {
	testFabricVLANSSIDSequence(t, true, true, true, true)
}
func testFabricVLANSSIDSequence(t *testing.T, public, mixed, retainPrevious bool, embeddedMode ...bool) {
	t.Helper()
	ctx := context.Background()
	a, config, physical, native := fabricVLANSSIDSequenceFixture(t, embeddedMode...)
	multicastEnabled := true
	update := func(vlan string) {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(fmt.Sprintf(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"wireless_ssids":[{"name":"Staff","vlan_name":%q},{"name":"Guest","vlan_name":"Clients"}]}]}}}`, vlan))
		if mixed {
			config.Spec.Source.Inline.Raw = []byte(fmt.Sprintf(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"wireless_ssids":[{"name":"Staff","vlan_name":"Clients"}],"multicast":{"wireless_multicast_enabled":%t}}]}}}`, multicastEnabled))
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	update("Clients")
	authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
	steps := 3
	if mixed {
		steps = 4
	}
	var previousMembers []ops.ControllerConfigurationReservation
	for index := 1; index <= steps; index++ {
		if mixed && index >= 3 {
			multicastEnabled = index == 4
			update("Clients")
		} else if index == 3 {
			update("New")
		}
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range physical {
			for _, obj := range []ctrlclient.Object{member.device, member.node} {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj); err != nil {
					t.Fatal(err)
				}
			}
		}
		if public {
			a.configurationAdmission = nil
			testPublicHandoffPreparation(t, a, config)
			configurationGroupDispatchFixture(t, a, physical, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			a.configurationAdmission = nil
			if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{"Verifying", "Verified"} {
				if retainPrevious && index == 3 && phase == "Verified" {
					before := journalState(t, a)
					// Reintroduce retired predecessor evidence after dispatch to exercise
					// the publication fence without changing the preparation fixtures.
					for i := range previousMembers {
						previousMembers[i].ResourceVersion = ""
						if err := a.statusWriter.Create(ctx, &previousMembers[i]); err != nil {
							t.Fatal(err)
						}
					}
					var all ops.ControllerConfigurationReservationList
					if err := a.statusReader.List(ctx, &all); err != nil {
						t.Fatal(err)
					}
					var current []*ops.ControllerConfigurationReservation
					for i := range all.Items {
						if all.Items[i].Spec.OperationID == before.Operation.ID {
							current = append(current, all.Items[i].DeepCopy())
						}
					}
					if err := a.recordFabricWirelessMulticastApplication(ctx, config, before.Operation.ID, "Global/Lab", current, authorize, time.Now()); err == nil {
						t.Fatal("replaced receipt while predecessor reservations remain")
					}
					if !reflect.DeepEqual(before, journalState(t, a)) {
						t.Fatal("refused publication changed journal")
					}
					for i := range previousMembers {
						if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, &previousMembers[i], func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
							t.Fatal("delayed retirement", err)
						}
					}
				}
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if journalState(t, a).Operation.Phase != phase {
					t.Fatal("public recovery", index, journalState(t, a).Operation.Phase)
				}
			}
		} else {
			op, err := a.prepareFabricVLANSSIDs(ctx, config, source, "Global/Lab")
			if err != nil || op == nil {
				t.Fatal("prepare", index, err)
			}
			permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
			if _, err := a.dispatchFabricVLANSSIDs(ctx, config, source, "Global/Lab", permit); err != nil {
				t.Fatal("dispatch", index, err)
			}
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			members := []*ops.ControllerConfigurationReservation{}
			for i := range list.Items {
				if list.Items[i].Status.Grant.Phase == "Held" {
					members = append(members, list.Items[i].DeepCopy())
				}
			}
			if err := a.beginFabricVLANSSIDVerification(ctx, config, source, "Global/Lab", members, authorize, time.Now()); err != nil {
				t.Fatal("recover", index, err)
			}
			if err := a.recordFabricVLANSSIDApplication(ctx, config, op.ID, "Global/Lab", members, authorize, time.Now()); err != nil {
				t.Fatal("publish", index, err)
			}
		}
		expected := index
		if expected > 2 {
			expected = 2
		}
		if len(journalState(t, a).ActionApplications) != expected {
			t.Fatal("receipt lost or duplicated")
		}
		a.configurationAdmission = nil
		testFabricHandoffCompletion(t, a, config, true)
		// Simulate the manager retirement actor through its real shared contract;
		// released reservations still require the predecessor receipt until removed.
		var settled ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &settled); err != nil {
			t.Fatal(err)
		}
		for i := range settled.Items {
			if retainPrevious && index == 2 {
				previousMembers = append(previousMembers, *settled.Items[i].DeepCopy())
			}
			if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, &settled.Items[i], func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
				t.Fatal("retirement", err)
			}
		}
		var leases coordination.LeaseList
		if err := a.statusReader.List(ctx, &leases); err != nil {
			t.Fatal(err)
		}
		for i := range leases.Items {
			if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
				t.Fatal(err)
			}
		}
		if public {
			// Remove simulated worker objects so the next fixture can install fresh
			// actor evidence. This is confined to the fake Kubernetes client.
			for _, object := range []ctrlclient.Object{&apps.Deployment{}, &apps.ReplicaSet{}, &core.Pod{}} {
				if err := a.statusWriter.DeleteAllOf(ctx, object, ctrlclient.InNamespace(config.Namespace)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if native.posts != steps || journalState(t, a).Operation != nil {
		t.Fatal("sequence incomplete")
	}
	observed, err := a.client.observeFabricVLANSSIDs(ctx, "Global/Lab")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := fabricVLANSSIDOwnership("Global/Lab", observed.siteID, observed.fabricID, string(config.UID), observed.mappings, journalState(t, a).ActionApplications)
	expectedOwned := 2
	staffVLAN := "New"
	if mixed {
		expectedOwned = 1
		staffVLAN = "Clients"
	}
	if err != nil || len(owned) != expectedOwned || owned["Staff"].VLAN != staffVLAN || (!mixed && owned["Guest"].VLAN != "Clients") || (mixed && !native.multicast) {
		t.Fatal("final ownership", owned, err)
	}
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("converged intent requested permit")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || native.posts != steps {
			t.Fatal("replayed or unconverged", config.Status.Phase)
		}
	}
	if public && !mixed {
		update("Clients")
		tag := "ExternalPolicy"
		for i := range native.rows {
			for j := range native.rows[i].SSIDs {
				if native.rows[i].SSIDs[j].Name == "Guest" {
					native.rows[i].SSIDs[j].SecurityGroup = &tag
				}
			}
		}
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if native.posts != steps || journalState(t, a).Operation != nil {
			t.Fatal("external policy change allowed partial update")
		}
	}

}
