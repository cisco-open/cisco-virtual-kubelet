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
	"k8s.io/apimachinery/pkg/api/equality"
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
	device, observation := topologyObservationFixture()
	apiClient := newTopologyObservationClient(t, device)
	if err := PublishNetworkObservation(context.Background(), apiClient,
		types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, device.UID, observation); err != nil {
		t.Fatalf("exact binding rejected: %v", err)
	}
	var updated ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &updated); err != nil {
		t.Fatal(err)
	}
	wantStatus := device.Status.DeepCopy()
	wantStatus.HealthObservation.Network = observation.DeepCopy()
	if !equality.Semantic.DeepEqual(updated.Status, *wantStatus) {
		t.Fatalf("publication changed fields outside network evidence: got %#v, want %#v", updated.Status, *wantStatus)
	}
	if err := PublishNetworkObservation(context.Background(), apiClient,
		client.ObjectKeyFromObject(device), device.UID, observation); err == nil {
		t.Fatal("replayed observation was accepted")
	}
	newer := observation.DeepCopy()
	newer.SampleSequence = 2
	if err := PublishNetworkObservation(context.Background(), apiClient,
		client.ObjectKeyFromObject(device), device.UID, newer); err != nil {
		t.Fatalf("newer observation rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice, *ciskov1.DeviceNetworkObservationStatus)
		want   string
	}{
		{"missing binding", func(d *ciskov1.CiscoDevice, _ *ciskov1.DeviceNetworkObservationStatus) {
			d.Status.NetworkWorkerRevision = nil
		}, "binding is absent"},
		{"replacement device", func(d *ciskov1.CiscoDevice, _ *ciskov1.DeviceNetworkObservationStatus) { d.UID = "replacement" }, "device UID changed"},
		{"missing Node binding", func(d *ciskov1.CiscoDevice, _ *ciskov1.DeviceNetworkObservationStatus) { d.Status.NodeIdentity = nil }, "not manager-bound"},
		{"revision rollout pending", func(d *ciskov1.CiscoDevice, _ *ciskov1.DeviceNetworkObservationStatus) {
			d.Status.NetworkWorkerRevision.DesiredRevision = "sha256:" + strings.Repeat("b", 64)
		}, "not at the desired revision"},
		{"Pod not ready", func(d *ciskov1.CiscoDevice, _ *ciskov1.DeviceNetworkObservationStatus) {
			d.Status.NetworkWorkerRevision.PodReadyTime = nil
		}, "no valid ready Pod"},
		{"stale revision", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.ProducerRevision = "sha256:" + strings.Repeat("b", 64)
		}, "producer revision"},
		{"wrong Pod", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.WorkerPodUID = "old-pod-uid"
		}, "worker Pod UID"},
		{"missing Pod", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) { o.WorkerPodUID = "" }, "worker Pod UID"},
		{"wrong physical identity", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.DeviceIdentityHash = identityHash("other-device")
		}, "device identity"},
		{"missing provenance", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.CollectionStartedAt = metav1.Time{}
		}, "missing collection provenance"},
		{"reversed interval", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.CollectionEndedAt = metav1.NewTime(o.CollectionStartedAt.Add(-time.Second))
		}, "interval is invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, o := device.DeepCopy(), observation.DeepCopy()
			tc.mutate(d, o)
			c := newTopologyObservationClient(t, d)
			before := d.Status.DeepCopy()
			err := PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), device.UID, o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			var after ciskov1.CiscoDevice
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(d), &after); err != nil {
				t.Fatal(err)
			}
			if !equality.Semantic.DeepEqual(after.Status, *before) {
				t.Fatal("rejected publication changed persisted status")
			}
		})
	}
}

func TestPublishNetworkObservationRestartSequenceRecovery(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacementPod=%t", replacement), func(t *testing.T) {
			d, o := topologyObservationFixture()
			previous := o.DeepCopy()
			previous.SampleSequence = 10000
			d.Status.HealthObservation.Network = previous
			o.SampleSequence = 0
			if replacement {
				d.Status.NetworkWorkerRevision.PodUID = "replacement-pod"
				o.WorkerPodUID = "replacement-pod"
			}
			c := newTopologyObservationClient(t, d)
			err := PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), d.UID, o)
			if err != nil {
				t.Fatal(err)
			}
			var got ciskov1.CiscoDevice
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(d), &got); err != nil {
				t.Fatal(err)
			}
			want := uint64(1)
			if !replacement {
				want = 10001
			}
			if got.Status.HealthObservation == nil || got.Status.HealthObservation.Network == nil ||
				got.Status.HealthObservation.Network.SampleSequence != want {
				t.Fatalf("accepted sequence=%v, want %d", got.Status.HealthObservation.Network, want)
			}
		})
	}
}

func TestPublishNetworkObservationRejectsSequenceExhaustion(t *testing.T) {
	d, o := topologyObservationFixture()
	d.Status.HealthObservation.Network = o.DeepCopy()
	d.Status.HealthObservation.Network.SampleSequence = ^uint64(0)
	o.SampleSequence = 0
	c := newTopologyObservationClient(t, d)
	if err := PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), d.UID, o); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("sequence exhaustion error = %v", err)
	}
}

func topologyObservationFixture() (*ciskov1.CiscoDevice, *ciskov1.DeviceNetworkObservationStatus) {
	const revision = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := metav1.NewTime(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC))
	started := metav1.NewTime(now.Add(-time.Minute))
	ready := metav1.NewTime(now.Add(-30 * time.Second))
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "lab", Name: "device-a", UID: types.UID("device-uid"), ResourceVersion: "1",
	}}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		NodeName: "device-a", NodeUID: "node-uid", DeviceUID: "device-uid", PhysicalIdentity: "serial-01",
	}
	device.Status.NetworkWorkerRevision = &ciskov1.DeviceNetworkWorkerRevisionStatus{
		DesiredRevision: revision, ObservedRevision: revision, DeploymentUID: "deployment-uid",
		DeploymentGeneration: 3, PodUID: "network-pod-uid", PodStartTime: &started, PodReadyTime: &ready,
		ObservedAt: now,
	}
	observation := &ciskov1.DeviceNetworkObservationStatus{
		WorkerPodUID: "network-pod-uid", CollectionStartedAt: metav1.NewTime(now.Add(-time.Second)),
		CollectionEndedAt: now, SampleSequence: 1, ObservedAt: now, Complete: true,
		ProducerRevision: revision, DeviceIdentityHash: identityHash("serial-01"),
	}
	device.Spec = ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, Address: "192.0.2.10", Username: "admin", PhysicalIdentity: "serial-01"}
	device.Status.HealthObservation = &ciskov1.DeviceHealthObservationStatus{
		ObservedAt: now, NodeReadyHeartbeatTime: now, DeviceConditionsHash: identityHash("conditions"),
	}
	return device, observation
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
