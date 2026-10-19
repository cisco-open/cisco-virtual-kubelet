// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func groupCandidateFixture(t *testing.T) (*cvk.NetworkController, []GroupMemberSnapshot, *core.Pod, time.Time) {
	t.Helper()
	op, _ := groupIdentityFixture()
	var members []GroupMemberSnapshot
	var targets []GroupTargetIdentity
	var now time.Time
	for _, suffix := range []string{"b", "a"} {
		_, d, n, p, at := deviceBindingFixture(t)
		now = at
		d.Name = "device-" + suffix
		d.UID = types.UID(d.Name + "-uid")
		d.Spec.PhysicalIdentity = "serial-" + suffix
		n.Name = d.Name
		n.UID = types.UID("node-" + suffix)
		n.Status.NodeInfo.MachineID = d.Spec.PhysicalIdentity
		n.Status.NodeInfo.SystemUUID = d.Spec.PhysicalIdentity
		p.Name = "worker-" + suffix
		p.UID = types.UID(p.Name + "-uid")
		d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(d.UID), NodeName: n.Name, NodeUID: string(n.UID), PhysicalIdentity: d.Spec.PhysicalIdentity}
		d.Status.NetworkWorkerRevision.PodUID = string(p.UID)
		n.Annotations[managedprotocol.AnnotationDeviceName] = d.Name
		n.Annotations[managedprotocol.AnnotationDeviceUID] = string(d.UID)
		n.Annotations[managedprotocol.AnnotationNodeUID] = string(n.UID)
		members = append(members, GroupMemberSnapshot{d, n, p})
		targets = append(targets, GroupTargetIdentity{InventoryID: "inventory-" + suffix, PhysicalIdentity: d.Spec.PhysicalIdentity, Device: ops.ConfigurationReservationReference{Name: d.Name, UID: string(d.UID)}, Node: ops.ConfigurationReservationReference{Name: n.Name, UID: string(n.UID)}})
	}
	prepared, err := WithGroupIdentity(op, targets)
	if err != nil {
		t.Fatal(err)
	}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "cc", UID: "controller", Generation: 1}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: prepared}}}
	connector := &core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "connector", UID: "connector-uid"}, Spec: core.PodSpec{ServiceAccountName: "connector"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(now)}}}}
	return nc, members, connector, now
}

func TestGroupReservationCandidates(t *testing.T) {
	nc, members, connector, now := groupCandidateFixture(t)
	before := nc.DeepCopy()
	candidates, err := GroupReservationCandidates(nc, members, connector, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Spec.PhysicalIdentity != "serial-a" || candidates[1].Spec.PhysicalIdentity != "serial-b" || candidates[0].Name == candidates[1].Name {
		t.Fatal("incorrect complete ordered group", candidates)
	}
	for i, r := range candidates {
		if r.UID != "" || r.Spec.Protocol != GroupReservationProtocol || r.Spec.OperationDigest != nc.Status.Configuration.Operation.PhysicalGroup.Digest || r.Spec.InventoryID == nc.Status.Configuration.Operation.TargetID || !reflect.DeepEqual(r.Status, ops.ControllerConfigurationReservationStatus{}) {
			t.Fatal("lost group identity or gained authority")
		}
		r.UID = "test-persisted"
		if ValidateOperationBinding(r, nc.Status.Configuration.Operation) == nil {
			t.Fatal("group admitted as v1 operation")
		}
		member := members[1-i]
		if ValidateDeviceWorkerBinding(r, member.Device, member.Node, member.Worker, now) == nil {
			t.Fatal("group admitted as v1 device binding")
		}
		r.UID = ""
	}
	members[0], members[1] = members[1], members[0]
	again, err := GroupReservationCandidates(nc, members, connector, now)
	if err != nil || !reflect.DeepEqual(candidates, again) || !reflect.DeepEqual(before, nc) {
		t.Fatal("order changed identity or constructor mutated journal", err)
	}
	// A new complete group identity changes every member name, not only the
	// member changed. Old per-member evidence cannot be spliced into a new set.
	changed := nc.DeepCopy()
	changed.Status.Configuration.Operation.PhysicalGroup = nil
	changed.Status.Configuration.Operation.SourceVersion = "new"
	changed.Status.Configuration.Operation, err = WithGroupIdentity(changed.Status.Configuration.Operation, before.Status.Configuration.Operation.PhysicalGroup.Targets)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := GroupReservationCandidates(changed, members, connector, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := range newer {
		if newer[i].Name == candidates[i].Name {
			t.Fatal("old group member name reused")
		}
	}
}

func TestGroupReservationCandidatesRefusePartialOrStaleSet(t *testing.T) {
	for _, scenario := range []string{"missing", "extra", "duplicate", "replaced-device", "replaced-node", "replaced-worker", "unknown-worker-revision", "unready-worker", "unready-controller", "wrong-physical", "foreign-namespace", "invalid-digest", "submitted", "nil-controller", "nil-node"} {
		t.Run(scenario, func(t *testing.T) {
			nc, members, connector, now := groupCandidateFixture(t)
			switch scenario {
			case "missing":
				members = members[:1]
			case "extra":
				members = append(members, members[0])
			case "duplicate":
				members[1] = members[0]
			case "replaced-device":
				members[1].Device.UID = "replacement"
			case "replaced-node":
				members[1].Node.UID = "replacement"
			case "replaced-worker":
				members[1].Worker.UID = "replacement"
			case "unknown-worker-revision":
				members[1].Device.Status.NetworkWorkerRevision = nil
			case "unready-worker":
				members[1].Worker.Status.Conditions = nil
			case "unready-controller":
				connector.Status.Conditions = nil
			case "wrong-physical":
				members[1].Node.Status.NodeInfo.MachineID = "replacement"
			case "foreign-namespace":
				members[1].Device.Namespace = "other"
			case "invalid-digest":
				nc.Status.Configuration.Operation.PhysicalGroup.Digest = "invalid"
			case "submitted":
				nc.Status.Configuration.Operation.Phase = "Submitting"
			case "nil-controller":
				nc = nil
			case "nil-node":
				members[1].Node = nil
			}
			result, err := GroupReservationCandidates(nc, members, connector, now)
			if err == nil || result != nil {
				t.Fatal("returned partial/invalid group", err, result)
			}
		})
	}
}
