// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package provider

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	cisco "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// This is the exact last branch schema with neighbors keyed by display ID.
// CI fetches it explicitly; missing historical input is a failure, never a skip.
const preAtomicNeighborCommit = "44d02a4b1f17c832e79ea554e7f77795e4472fc4"

func TestEnvtest_TopologyStoredNeighborMigration(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	if err := apiext.AddToScheme(c.Scheme()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var current apiext.CustomResourceDefinition
	key := types.NamespacedName{Name: "ciscodevices.cisco.vk"}
	if err := c.Get(ctx, key, &current); err != nil {
		t.Fatal(err)
	}
	data, err := exec.CommandContext(ctx, "git", "show", preAtomicNeighborCommit+":config/crd/cisco.vk_ciscodevices.yaml").Output()
	if err != nil {
		t.Fatalf("read pinned pre-atomic CRD (fetch %s): %v", preAtomicNeighborCommit, err)
	}
	var previous apiext.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &previous); err != nil {
		t.Fatal(err)
	}
	install := func(spec apiext.CustomResourceDefinitionSpec) {
		t.Helper()
		var live apiext.CustomResourceDefinition
		if err := c.Get(ctx, key, &live); err != nil {
			t.Fatal(err)
		}
		live.Spec = *spec.DeepCopy()
		if err := c.Update(ctx, &live); err != nil {
			t.Fatal(err)
		}
	}
	install(previous.Spec)
	const ns = "envtest-stored-migration"
	envtestNamespace(t, c, ns)
	device := &cisco.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: ns},
		Spec: cisco.DeviceSpec{Driver: cisco.DeviceDriverXE, Address: "192.0.2.1", Username: "test"}}
	if err := c.Create(ctx, device); err != nil {
		t.Fatal(err)
	}
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	device.Status.HealthObservation = &cisco.DeviceHealthObservationStatus{
		ObservedAt: now, NodeReadyHeartbeatTime: now, DeviceConditionsHash: "sha256:" + strings.Repeat("a", 64),
		Network: &cisco.DeviceNetworkObservationStatus{ObservedAt: now, Complete: true,
			ProducerRevision: "old-producer", DeviceIdentityHash: "sha256:" + strings.Repeat("b", 64),
			Neighbors: []cisco.DeviceNetworkNeighborObservation{{ID: "peer", State: "Full", Source: "cdp", Interface: "Gi1/0/1"}}},
	}
	if err := c.Status().Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	// Prove the historical schema is actually serving, not merely persisted:
	// CRD serving changes are asynchronous. The old map list must reject two
	// interfaces whose neighbors have the same display ID.
	oldNeighbors := append([]cisco.DeviceNetworkNeighborObservation(nil), device.Status.HealthObservation.Network.Neighbors...)
	deadline := time.Now().Add(10 * time.Second)
	for {
		device.Status.HealthObservation.Network.Neighbors = append(append([]cisco.DeviceNetworkNeighborObservation(nil), oldNeighbors...),
			cisco.DeviceNetworkNeighborObservation{ID: "peer", State: "Full", Source: "cdp", Interface: "Gi1/0/2"})
		err = c.Status().Update(ctx, device)
		if apierrors.IsInvalid(err) && strings.Contains(err.Error(), "Duplicate value") {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("historical schema never rejected duplicate display IDs")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}
	device.Status.HealthObservation.Network.Neighbors = oldNeighbors
	if err := c.Status().Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	uid := device.UID
	install(current.Spec)
	if err := c.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}
	if device.UID != uid || len(device.Status.HealthObservation.Network.Neighbors) != 1 {
		t.Fatal("schema upgrade lost stored device or neighbor")
	}
	// Two physical adjacencies share a display name, not a canonical identity.
	device.Status.HealthObservation.Network.Neighbors = []cisco.DeviceNetworkNeighborObservation{
		{Identity: "cdp/gi1/0/1/peer", ID: "peer", State: "Full", Source: "cdp", Interface: "Gi1/0/1"},
		{Identity: "cdp/gi1/0/2/peer", ID: "peer", State: "Full", Source: "cdp", Interface: "Gi1/0/2"},
	}
	// CRD serving updates are asynchronous; retry only validation rejection,
	// never turn a missing assertion or unrelated API error into a pass.
	deadline = time.Now().Add(10 * time.Second)
	for {
		err = c.Status().Update(ctx, device)
		if err == nil {
			break
		}
		if time.Now().After(deadline) || !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "Duplicate value") {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}
	neighbors := device.Status.HealthObservation.Network.Neighbors
	if len(neighbors) != 2 || neighbors[0].Identity == neighbors[1].Identity {
		t.Fatalf("source-qualified neighbors lost: %+v", neighbors)
	}
	// Re-read persisted data; migration must not manufacture fresh authority.
	var reread cisco.CiscoDevice
	if err := c.Get(ctx, client.ObjectKeyFromObject(device), &reread); err != nil {
		t.Fatal(err)
	}
	if reread.Status.HealthObservation.AcceptedNetwork != nil {
		t.Fatal("schema migration manufactured accepted authority")
	}
	if reread.Status.HealthObservation.Network.SampleSequence != 0 {
		t.Fatal("schema migration manufactured fresh provenance")
	}
}
