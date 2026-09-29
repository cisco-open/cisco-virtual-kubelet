// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagedUpgradeRequiresNetworkWorkerEvidence(t *testing.T) {
	now := metav1.NewTime(time.Now())
	device := &ciskov1.CiscoDevice{}
	device.Status.WorkerRevision = &ciskov1.DeviceWorkerRevisionStatus{
		DesiredRevision: "app", ObservedRevision: "app", PodUID: "app-pod",
		PodStartTime: &now, ReadyHeartbeatTime: &now,
	}
	device.Status.NetworkWorkerRevision = &ciskov1.DeviceNetworkWorkerRevisionStatus{
		DesiredRevision: "network", ObservedRevision: "network", PodUID: "network-pod",
		PodStartTime: &now, PodReadyTime: &now,
	}
	if !managedUpgradeWorkerReady(device, "network", "network-pod") {
		t.Fatal("ready network worker rejected because app revision differs")
	}
	if managedUpgradeWorkerReady(device, "app", "app-pod") {
		t.Fatal("app worker substituted for the network worker")
	}
	device.Status.NetworkWorkerRevision.ObservedRevision = "old"
	if managedUpgradeWorkerReady(device, "network", "network-pod") {
		t.Fatal("stale network worker accepted")
	}
	if managedUpgradeWorkerReady(device, "app", "app-pod") {
		t.Fatal("unready network worker fell back to app authority")
	}
}
