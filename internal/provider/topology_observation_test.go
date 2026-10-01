package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type observationTopologyProvider struct {
	interfaces []common.InterfaceStats
	cdp        []common.CDPNeighbor
	ospf       []common.OSPFNeighbor
	err        error
}

func (p observationTopologyProvider) GetCDPNeighbors(context.Context) ([]common.CDPNeighbor, error) {
	return p.cdp, p.err
}
func (p observationTopologyProvider) GetOSPFNeighbors(context.Context) ([]common.OSPFNeighbor, error) {
	return p.ospf, p.err
}
func (p observationTopologyProvider) GetInterfaceStats(context.Context) ([]common.InterfaceStats, error) {
	return p.interfaces, p.err
}
func (p observationTopologyProvider) GetInterfaceIPs(context.Context) ([]common.InterfaceIP, error) {
	return nil, nil
}
func (p observationTopologyProvider) GetHostedApps(context.Context) ([]common.HostedApp, error) {
	return nil, nil
}

func TestBuildNetworkObservationIsBoundedAndDeterministic(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi2", OperStatus: "down"}, {Name: "Gi1", OperStatus: "up"}},
		cdp:        []common.CDPNeighbor{{DeviceID: "peer-a"}},
		ospf:       []common.OSPFNeighbor{{NeighborID: "peer-a", State: "FULL"}, {NeighborID: "peer-b", State: "2way"}},
	}, "SERIAL-01", "sha256:worker", now, "pod-uid")
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Complete || observation.DeviceIdentityHash == "" || observation.WorkerPodUID != "pod-uid" {
		t.Fatalf("observation=%#v", observation)
	}
	if got := observation.Interfaces[0].Name; got != "Gi1" {
		t.Fatalf("interfaces not sorted: %#v", observation.Interfaces)
	}
	if len(observation.Neighbors) != 3 {
		t.Fatalf("neighbors with distinct discovery identities = %#v, want 3", observation.Neighbors)
	}
	for _, neighbor := range observation.Neighbors {
		if neighbor.Identity == "" || !strings.Contains(neighbor.Identity, "|") {
			t.Fatalf("neighbor identity is not source/context-qualified: %#v", neighbor)
		}
	}
}

func TestBuildNetworkObservationFailsClosedOnPartialSource(t *testing.T) {
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{err: errors.New("unsupported")}, "SERIAL-01", "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || observation.UnknownReason == "" {
		t.Fatalf("partial observation=%#v", observation)
	}
}

func TestBuildNetworkObservationRejectsTruncationAndDuplicates(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	interfaces := make([]common.InterfaceStats, 65)
	for i := range interfaces {
		interfaces[i].Name = fmt.Sprintf("Gi1/0/%d", i+1)
	}
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: interfaces,
	}, "SERIAL-01", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || !strings.Contains(observation.UnknownReason, "limit") {
		t.Fatalf("expected truncation to be incomplete, got %#v", observation)
	}

	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1"}, {Name: "Gi1"}},
	}, "SERIAL-01", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || !strings.Contains(observation.UnknownReason, "duplicate interface") {
		t.Fatalf("expected duplicate to be incomplete, got %#v", observation)
	}
}

func TestBuildNetworkObservationPublishesConservativeHeadroom(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", Speed: 1000, InBitsPerSec: 250, OutBitsPerSec: 100,
			InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true}},
	}, "SERIAL-01", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Interfaces[0].HeadroomPercent == nil || *observation.Interfaces[0].HeadroomPercent != 75 {
		t.Fatalf("headroom=%v, want 75%%", observation.Interfaces[0].HeadroomPercent)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", InBitsPerSec: 1}},
	}, "SERIAL-01", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Interfaces[0].HeadroomPercent != nil {
		t.Fatalf("missing speed must remain unknown, got %v", *observation.Interfaces[0].HeadroomPercent)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", Speed: 1000, InBitsPerSec: 0, OutBitsPerSec: 0,
			InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true}},
	}, "SERIAL-01", "worker", now)
	if err != nil || observation.Interfaces[0].HeadroomPercent == nil || *observation.Interfaces[0].HeadroomPercent != 100 {
		t.Fatalf("measured zero rates must produce 100%% headroom, got %#v err=%v", observation.Interfaces[0].HeadroomPercent, err)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", Speed: 1000, InBitsPerSec: 250,
			InRatePresent: true, InRateValid: true}},
	}, "SERIAL-01", "worker", now)
	if err != nil || observation.Interfaces[0].HeadroomPercent != nil {
		t.Fatalf("one missing direction must remain unknown, got %#v err=%v", observation.Interfaces[0].HeadroomPercent, err)
	}
}

func TestPublishNetworkObservationRequiresExactManagedWorkerBinding(t *testing.T) {
	const revision = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := metav1.NewTime(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC))
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "lab", Name: "device-a", UID: types.UID("device-uid"), ResourceVersion: "1",
	}}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		NodeName: "device-a", NodeUID: "node-uid", DeviceUID: "device-uid", PhysicalIdentity: "SERIAL-01",
	}
	device.Status.NetworkWorkerRevision = &ciskov1.DeviceNetworkWorkerRevisionStatus{
		DesiredRevision: revision, ObservedRevision: revision, DeploymentUID: "deployment-uid",
		DeploymentGeneration: 3, PodUID: "network-pod-uid", PodStartTime: &now, PodReadyTime: &now,
		ObservedAt: now,
	}
	observation := &ciskov1.DeviceNetworkObservationStatus{
		WorkerPodUID: "network-pod-uid", CollectionStartedAt: metav1.NewTime(now.Add(-time.Second)),
		CollectionEndedAt: now, SampleSequence: 1, ObservedAt: now, Complete: true,
		ProducerRevision: revision, DeviceIdentityHash: identityHash("serial-01"),
	}
	apiClient := newTopologyObservationClient(t, device)
	if err := PublishNetworkObservation(context.Background(), apiClient,
		types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, device.UID, observation); err != nil {
		t.Fatalf("exact binding rejected: %v", err)
	}
	var updated ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.HealthObservation == nil || updated.Status.HealthObservation.Network == nil {
		t.Fatal("exact binding did not publish network observation")
	}
	if err := PublishNetworkObservation(context.Background(), apiClient,
		types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, device.UID, observation); err == nil {
		t.Fatal("replayed observation was accepted")
	}
	newer := observation.DeepCopy()
	newer.SampleSequence = 2
	if err := PublishNetworkObservation(context.Background(), apiClient,
		types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, device.UID, newer); err != nil {
		t.Fatalf("newer observation rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice, *ciskov1.DeviceNetworkObservationStatus)
	}{
		{name: "missing binding", mutate: func(d *ciskov1.CiscoDevice, _ *ciskov1.DeviceNetworkObservationStatus) {
			d.Status.NetworkWorkerRevision = nil
		}},
		{name: "stale revision", mutate: func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.ProducerRevision = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "wrong Pod", mutate: func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.WorkerPodUID = "old-pod-uid"
		}},
		{name: "wrong physical identity", mutate: func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.DeviceIdentityHash = identityHash("other-device")
		}},
		{name: "missing provenance", mutate: func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.SampleSequence = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := device.DeepCopy()
			o := observation.DeepCopy()
			tc.mutate(d, o)
			if err := PublishNetworkObservation(context.Background(), newTopologyObservationClient(t, d),
				types.NamespacedName{Namespace: d.Namespace, Name: d.Name}, d.UID, o); err == nil {
				t.Fatal("mismatched publisher evidence was accepted")
			}
		})
	}
}

func newTopologyObservationClient(t *testing.T, device *ciskov1.CiscoDevice) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).WithObjects(device.DeepCopy()).Build()
}
