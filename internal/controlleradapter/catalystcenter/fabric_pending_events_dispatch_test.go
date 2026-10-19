// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
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
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// This fixture supplies real shared grant/claim/lease evidence. Its authorizer
// checks a scheduling guard; installed admission and native workload attestation
// are simulated, not proof of a deployed worker/manager integration.
func pendingEventDispatchPermitFixture(t *testing.T, a *adapter, device *cvk.CiscoDevice, node *core.Node) (*contract.GroupSubmissionPermit, func()) {
	return configurationGroupDispatchPermitFixture(t, a, []configurationDispatchFixtureMember{{device, node}})
}

type configurationDispatchFixtureMember struct {
	device *cvk.CiscoDevice
	node   *core.Node
}

func configurationGroupDispatchPermitFixture(t *testing.T, a *adapter, members []configurationDispatchFixtureMember) (*contract.GroupSubmissionPermit, func()) {
	return configurationGroupDispatchFixture(t, a, members, true)
}

func configurationGroupDispatchFixture(t *testing.T, a *adapter, members []configurationDispatchFixtureMember, claim bool, reuseRetained ...bool) (*contract.GroupSubmissionPermit, func()) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	started, ready := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(-time.Second))
	snapshots := []contract.GroupMemberSnapshot{}
	var connector *core.Pod
	for i, member := range members {
		device, node := member.device, member.node
		d := device.DeepCopy()
		workerUID := types.UID(fmt.Sprintf("worker-%d-uid", i))
		d.Generation = 1
		d.Status.NetworkWorkerRevision = &cvk.DeviceNetworkWorkerRevisionStatus{DesiredRevision: "revision", ObservedRevision: "revision", PodUID: string(workerUID), PodStartTime: &started, PodReadyTime: &ready}
		n := node.DeepCopy()
		n.Annotations = map[string]string{managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: d.Namespace, managedprotocol.AnnotationDeviceName: d.Name, managedprotocol.AnnotationDeviceUID: string(d.UID), managedprotocol.AnnotationNodeUID: string(n.UID), managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version, managedprotocol.AnnotationNetworkWorkerUsername: "system:serviceaccount:" + d.Namespace + ":network"}
		n.Spec.Taints = []core.Taint{{Key: managedprotocol.MaintenanceTaintKey, Value: managedprotocol.MaintenanceTaintValue, Effect: core.TaintEffectNoSchedule}}
		if err := a.statusWriter.Update(ctx, n); err != nil {
			t.Fatal(err)
		}
		pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("worker-%d", i), Namespace: d.Namespace, UID: workerUID, Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: "revision"}}, Spec: core.PodSpec{ServiceAccountName: "network"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
		connector = pod.DeepCopy()
		connector.Name, connector.UID, connector.Spec.ServiceAccountName = "connector", "connector-uid", "connector"
		snapshots = append(snapshots, contract.GroupMemberSnapshot{Device: d, Node: n, Worker: pod})
	}
	reservations, err := contract.GroupReservationCandidates(nc, snapshots, connector, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reservations {
		r.UID = types.UID(r.Name + "-uid")
		holder, err := contract.Holder(r.Namespace, r)
		if err != nil {
			t.Fatal(err)
		}
		name := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
		duration, transitions := int32(60), int32(1)
		acquired, renewed := metav1.NewMicroTime(now.Add(-30*time.Second)), metav1.NewMicroTime(now.Add(-time.Second))
		lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: name, UID: types.UID(name + "-uid")}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, LeaseTransitions: &transitions, AcquireTime: &acquired, RenewTime: &renewed}}
		r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: name, UID: string(lease.UID)}, Holder: holder, LeaseTransitions: transitions, GrantedAt: metav1.NewTime(now.Add(-20 * time.Second))}
		if err := coordination.AddToScheme(a.statusWriter.Scheme()); err != nil {
			t.Fatal(err)
		}
		if len(reuseRetained) > 0 && reuseRetained[0] {
			var old coordination.Lease
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(lease), &old); err != nil {
				t.Fatal(err)
			}
			if old.Spec.HolderIdentity != nil && *old.Spec.HolderIdentity != "" {
				t.Fatal("next fixture grant cannot replace a live holder")
			}
			lease.UID, lease.ResourceVersion, lease.Annotations = old.UID, old.ResourceVersion, old.Annotations
			if old.Spec.LeaseTransitions == nil {
				t.Fatal("missing retained lease incarnation")
			}
			next := *old.Spec.LeaseTransitions + 1
			lease.Spec.LeaseTransitions = &next
			r.Status.Grant.Lease.UID = string(old.UID)
			r.Status.Grant.LeaseTransitions = next
			if err := a.statusWriter.Update(ctx, lease); err != nil {
				t.Fatal(err)
			}
		} else if err := a.statusWriter.Create(ctx, lease); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
	}
	authorize := func(ctx context.Context, _ []*ops.ControllerConfigurationReservation) error {
		for _, member := range members {
			var fresh core.Node
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member.node), &fresh); err != nil {
				return err
			}
			guarded := false
			for _, taint := range fresh.Spec.Taints {
				if taint.Key == managedprotocol.MaintenanceTaintKey && taint.Effect == core.TaintEffectNoSchedule {
					guarded = true
				}
			}
			if !guarded {
				return errors.New("missing fixture scheduling guard")
			}
		}
		return nil
	}
	var permit *contract.GroupSubmissionPermit
	if claim {
		permit, err = contract.ClaimGroupForController(ctx, a.statusReader, a.statusWriter, nc, reservations, "leases", authorize, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	return permit, func() {
		var fresh ops.ControllerConfigurationReservation
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(reservations[len(reservations)-1]), &fresh); err != nil {
			t.Error(err)
			return
		}
		fresh.Status.Grant.Phase = "Revoked"
		if err := a.statusWriter.Status().Update(ctx, &fresh); err != nil {
			t.Error(err)
		}
	}
}
