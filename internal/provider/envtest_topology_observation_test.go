// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package provider

import (
	"context"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This exercises CRD validation and status persistence with an admin client.
// startEnvtest does not install the Helm ValidatingAdmissionPolicies or issue
// bound worker tokens. E01-C/E still require those separate authorization tests.
func TestEnvtest_NetworkObservationStatusRoundTrip(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-network-observation")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, sample := topologyObservationFixture()
	d.Namespace = "envtest-network-observation"
	d.UID, d.ResourceVersion = "", ""
	status := d.Status.DeepCopy()
	d.Status = ciskov1.DeviceStatus{}
	if err := c.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	status.NodeIdentity.DeviceUID = string(d.UID)
	d.Status = *status
	if err := c.Status().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(d)
	// Compare the round-tripped manager status, including server defaults.
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	baseline := d.Status.DeepCopy()
	for _, complete := range []bool{true, false} {
		sample.Complete = complete
		if !complete {
			sample.SampleSequence++
			sample.UnknownReason = "unavailable sources: interfaces"
		}
		if err := PublishNetworkObservation(ctx, c, key, d.UID, sample); err != nil {
			t.Fatal(err)
		}
		var got ciskov1.CiscoDevice
		if err := c.Get(ctx, key, &got); err != nil {
			t.Fatal(err)
		}
		if !equality.Semantic.DeepEqual(got.Status.HealthObservation.Network, sample) {
			t.Fatalf("sample changed across API round trip: %#v", got.Status.HealthObservation.Network)
		}
		got.Status.HealthObservation.Network = nil
		if !equality.Semantic.DeepEqual(got.Status, *baseline) {
			t.Fatal("manager status changed during publication")
		}
	}
	// Malformed source identities are reduced to bounded reason classes before
	// publication; the API must accept the incomplete evidence and retain the
	// prior manager-owned fields.
	name := "peer-with-an-untrusted-long-name"
	oversized, err := BuildNetworkObservation(ctx, observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: name}, {Name: name}},
		cdp:        []common.CDPNeighbor{{DeviceID: name}, {DeviceID: name}},
	}, "serial-01", sample.ProducerRevision, time.Now(), sample.WorkerPodUID)
	if err != nil {
		t.Fatal(err)
	}
	if oversized.Complete || oversized.UnknownReason == "" || len(oversized.UnknownReason) > 256 {
		t.Fatalf("malformed-source evidence is not bounded incomplete: %#v", oversized)
	}
	oversized.SampleSequence = 0
	if err := PublishNetworkObservation(ctx, c, key, d.UID, oversized); err != nil {
		t.Fatalf("bounded incomplete evidence should be accepted: %v", err)
	}
	var after ciskov1.CiscoDevice
	if err := c.Get(ctx, key, &after); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(after.Status.HealthObservation.Network, oversized) {
		t.Fatalf("bounded malformed sample was not persisted: %#v", after.Status.HealthObservation.Network)
	}
}
