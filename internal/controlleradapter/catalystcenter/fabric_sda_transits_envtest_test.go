// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Real API identities/status are separate from the simulated manager repairs
// below. No installed actor, reservation, native lab or convergence is claimed.
func TestEnvtestSDATransitManagedIdentity(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/cisco.vk_ciscodevices.yaml")
	ctx := context.Background()
	c, want, f, fixture := newSDATransitFixture(t)
	for _, namespace := range []string{"lab", "other"} {
		if err := api.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
			t.Fatal(err)
		}
	}
	var devices cvk.CiscoDeviceList
	if err := fixture.List(ctx, &devices); err != nil {
		t.Fatal(err)
	}
	for _, source := range devices.Items {
		var fixtureNode corev1.Node
		if err := fixture.Get(ctx, ctrlclient.ObjectKey{Name: source.Status.NodeIdentity.NodeName}, &fixtureNode); err != nil {
			t.Fatal(err)
		}
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fixtureNode.Name}}
		if err := api.Create(ctx, node); err != nil {
			t.Fatal(err)
		}
		node.Status.NodeInfo.MachineID = fixtureNode.Status.NodeInfo.MachineID
		if err := api.Status().Update(ctx, node); err != nil {
			t.Fatal(err)
		}
		d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: source.Name}, Spec: source.Spec}
		if err := api.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(d.UID), NodeName: node.Name, NodeUID: string(node.UID), PhysicalIdentity: source.Spec.PhysicalIdentity}
		if err := api.Status().Update(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, targets, err := c.sdaTransitTargets(ctx, api, "lab", want)
	if err != nil || snapshot == nil || len(targets) != 2 {
		t.Fatal("API identities did not bind", err)
	}
	var replacement *corev1.Node
	write, _, err := c.planManagedSDATransitCreate(ctx, api, "lab", want, func(ctx context.Context, _ []Device) error {
		var node corev1.Node
		if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "edge0"}, &node); err != nil {
			return err
		}
		old := node.UID
		machine := node.Status.NodeInfo.MachineID
		if err := api.Delete(ctx, &node); err != nil {
			return err
		}
		replacement = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node.Name}}
		if err := api.Create(ctx, replacement); err != nil {
			return err
		}
		if replacement.UID == old {
			t.Fatal("API did not replace Node UID")
		}
		replacement.Status.NodeInfo.MachineID = machine
		return api.Status().Update(ctx, replacement)
	})
	if err != nil || write == nil {
		t.Fatal(err)
	}
	if _, err := write.submit(ctx); err == nil || f.posts != 0 {
		t.Fatal("late API Node replacement reached native mutation", err)
	}
	// The foundation forbids rebinding an existing managed identity. Confirm
	// that contract, then explicitly re-enrol a new fixture object; the connector
	// performs neither the status change nor this replacement lifecycle.
	var device cvk.CiscoDevice
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "edge0"}, &device); err != nil {
		t.Fatal(err)
	}
	device.Status.NodeIdentity.NodeUID = string(replacement.UID)
	if err := api.Status().Update(ctx, &device); err == nil {
		t.Fatal("foundation unexpectedly permitted immutable Node rebinding")
	}
	oldUID, spec := device.UID, device.Spec
	if err := api.Delete(ctx, &device); err != nil {
		t.Fatal(err)
	}
	device = cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "edge0"}, Spec: spec}
	if err := api.Create(ctx, &device); err != nil {
		t.Fatal(err)
	}
	if device.UID == oldUID {
		t.Fatal("fixture re-enrolment retained old device UID")
	}
	device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: replacement.Name, NodeUID: string(replacement.UID), PhysicalIdentity: spec.PhysicalIdentity}
	if err := api.Status().Update(ctx, &device); err != nil {
		t.Fatal(err)
	}
	write, _, err = c.planManagedSDATransitCreate(ctx, api, "lab", want, func(context.Context, []Device) error { return nil })
	if err != nil || write == nil {
		t.Fatal("explicit fixture re-enrolment did not establish fresh binding", err)
	}
	if _, err := write.submit(ctx); err != nil || f.posts != 1 {
		t.Fatal("managed HTTP fixture create failed", err)
	}
	alias := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "alias"}, Spec: device.Spec}
	if err := api.Create(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if state, targets, err := c.sdaTransitTargets(ctx, api, "lab", want); err == nil || state != nil || targets != nil {
		t.Fatal("real API cross-namespace alias admitted")
	}
	t.Log("real API identities, late Node replacement refusal, immutable rebind rejection, explicit fixture re-enrolment and cross-namespace alias rejection verified; one HTTP-fixture write, no public admission or convergence claim")
}
