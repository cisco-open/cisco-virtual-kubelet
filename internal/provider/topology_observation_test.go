package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type observationTopologyProvider struct {
	interfaces []common.InterfaceStats
	cdp        []common.CDPNeighbor
	ospf       []common.OSPFNeighbor
	err        error
}

type observationStatusClient struct {
	client.Client
	writer client.StatusWriter
}

func (c *observationStatusClient) Status() client.StatusWriter { return c.writer }

type barrierObservationStatusWriter struct {
	delegate client.StatusWriter
	arrived  chan struct{}
	release  <-chan struct{}
}

func (w *barrierObservationStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return w.delegate.Create(ctx, obj, subResource, opts...)
}

func (w *barrierObservationStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return w.delegate.Update(ctx, obj, opts...)
}

func (w *barrierObservationStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	w.arrived <- struct{}{}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.release:
		return w.delegate.Patch(ctx, obj, patch, opts...)
	}
}

func (w *barrierObservationStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return w.delegate.Apply(ctx, obj, opts...)
}

type lostResponseObservationStatusWriter struct {
	delegate client.StatusWriter
	lost     atomic.Bool
}

func (w *lostResponseObservationStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return w.delegate.Create(ctx, obj, subResource, opts...)
}

func (w *lostResponseObservationStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return w.delegate.Update(ctx, obj, opts...)
}

func (w *lostResponseObservationStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	err := w.delegate.Patch(ctx, obj, patch, opts...)
	if err == nil && w.lost.CompareAndSwap(false, true) {
		return errors.New("simulated lost status response")
	}
	return err
}

func (w *lostResponseObservationStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return w.delegate.Apply(ctx, obj, opts...)
}

type blockingObservationTopologyProvider struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (p *blockingObservationTopologyProvider) GetInterfaceStats(context.Context) ([]common.InterfaceStats, error) {
	p.calls.Add(1)
	p.once.Do(func() { close(p.started) })
	<-p.release // Deliberately ignores context to model a stuck device client.
	return nil, nil
}
func (p *blockingObservationTopologyProvider) GetCDPNeighbors(context.Context) ([]common.CDPNeighbor, error) {
	return nil, nil
}
func (p *blockingObservationTopologyProvider) GetOSPFNeighbors(context.Context) ([]common.OSPFNeighbor, error) {
	return nil, nil
}
func (p *blockingObservationTopologyProvider) GetInterfaceIPs(context.Context) ([]common.InterfaceIP, error) {
	return nil, nil
}
func (p *blockingObservationTopologyProvider) GetHostedApps(context.Context) ([]common.HostedApp, error) {
	return nil, nil
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
	}, "SERIAL-01", "sha256:worker", "pod-uid", now)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Complete || observation.DeviceIdentityHash == "" || observation.WorkerPodUID != "pod-uid" {
		t.Fatalf("observation=%#v", observation)
	}
	if !observation.ObservedAt.Time.Equal(now) || !observation.CollectionStartedAt.Time.Equal(now) ||
		observation.ObservedAt.After(observation.CollectionEndedAt.Time) {
		t.Fatalf("observation time must be the conservative collection start: %#v", observation)
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
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{err: errors.New("unsupported")}, "SERIAL-01", "worker", "pod-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || observation.UnknownReason == "" {
		t.Fatalf("partial observation=%#v", observation)
	}
}

func TestBuildNetworkObservationRequiresProducerPodIdentity(t *testing.T) {
	if _, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{}, "SERIAL-01", "worker", "", time.Now()); err == nil || !strings.Contains(err.Error(), "worker Pod UID is empty") {
		t.Fatalf("missing worker Pod UID error = %v", err)
	}
	if _, err := incompleteNetworkObservation("SERIAL-01", "worker", "", time.Now(), time.Now(), "incomplete"); err == nil || !strings.Contains(err.Error(), "worker Pod UID is empty") {
		t.Fatalf("missing incomplete-sample worker Pod UID error = %v", err)
	}
}

func TestNetworkObservationCollectorBoundsHungProvider(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	provider := &blockingObservationTopologyProvider{release: make(chan struct{}), started: make(chan struct{})}
	collector := &networkObservationCollector{}
	timedOut, cancel := context.WithCancel(context.Background())
	cancel()
	observation, err := collector.collect(timedOut, provider, "serial-01", "sha256:worker", "pod-a", now)
	if err != nil || observation.Complete || observation.UnknownReason != "collection deadline exceeded" {
		t.Fatalf("timed-out collection = %#v, %v", observation, err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("hung provider did not start")
	}
	observation, err = collector.collect(timedOut, provider, "serial-01", "sha256:worker", "pod-a", now.Add(time.Minute))
	if err != nil || observation.Complete || observation.UnknownReason != "collection remains in flight after deadline" {
		t.Fatalf("in-flight collection = %#v, %v", observation, err)
	}
	if !observation.ObservedAt.Time.Equal(now) {
		t.Fatalf("stuck collection advanced evidence time to %s, want %s", observation.ObservedAt.Time, now)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("hung collection started %d providers, want one", provider.calls.Load())
	}
	close(provider.release)
	deadline := time.Now().Add(time.Second)
	for {
		observation, err = collector.collect(context.Background(), provider, "serial-01", "sha256:worker", "pod-a", time.Now())
		if err == nil && observation.Complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector did not recover after the hung call returned: %#v, %v", observation, err)
		}
		time.Sleep(time.Millisecond)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("recovered collection calls=%d, want two", provider.calls.Load())
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
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || !strings.Contains(observation.UnknownReason, "limit") {
		t.Fatalf("expected truncation to be incomplete, got %#v", observation)
	}
	neighbors := make([]common.CDPNeighbor, 65)
	for i := range neighbors {
		neighbors[i].DeviceID = fmt.Sprintf("peer-%d", i)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		cdp: neighbors,
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || !strings.Contains(observation.UnknownReason, "limit") {
		t.Fatalf("expected neighbor truncation to be incomplete, got %#v", observation)
	}

	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1"}, {Name: "Gi1"}},
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || !strings.Contains(observation.UnknownReason, "duplicate identity") {
		t.Fatalf("expected duplicate to be incomplete, got %#v", observation)
	}
}

func TestBuildNetworkObservationPublishesConservativeHeadroom(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", Speed: 1000, InBitsPerSec: 250, OutBitsPerSec: 100,
			InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}},
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Interfaces[0].HeadroomPercent == nil || *observation.Interfaces[0].HeadroomPercent != 75 {
		t.Fatalf("headroom=%v, want 75%%", observation.Interfaces[0].HeadroomPercent)
	}
	item := observation.Interfaces[0]
	if item.CapacityBitsPerSecond == nil || *item.CapacityBitsPerSecond != 1000 ||
		item.IngressBitsPerSecond == nil || *item.IngressBitsPerSecond != 250 ||
		item.EgressBitsPerSecond == nil || *item.EgressBitsPerSecond != 100 ||
		item.RateSource != "fixture" {
		t.Fatalf("directional rate evidence = %#v", item)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", InBitsPerSec: 1}},
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Interfaces[0].HeadroomPercent != nil {
		t.Fatalf("missing speed must remain unknown, got %v", *observation.Interfaces[0].HeadroomPercent)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", Speed: 1000, InBitsPerSec: 0, OutBitsPerSec: 0,
			InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}},
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil || observation.Interfaces[0].HeadroomPercent == nil || *observation.Interfaces[0].HeadroomPercent != 100 {
		t.Fatalf("measured zero rates must produce 100%% headroom, got %#v err=%v", observation.Interfaces[0].HeadroomPercent, err)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: "Gi1", Speed: 1000, InBitsPerSec: 250,
			InRatePresent: true, InRateValid: true, RateSource: "fixture"}},
	}, "SERIAL-01", "worker", "pod-a", now)
	if err != nil || observation.Interfaces[0].HeadroomPercent != nil {
		t.Fatalf("one missing direction must remain unknown, got %#v err=%v", observation.Interfaces[0].HeadroomPercent, err)
	}
}

func TestInterfaceRateEvidenceFailsClosedOnUnqualifiedInputs(t *testing.T) {
	for _, test := range []struct {
		name  string
		stats common.InterfaceStats
		want  *int32
	}{
		{name: "empty source", stats: common.InterfaceStats{Speed: 1000, InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true}},
		{name: "capacity overflow", stats: common.InterfaceStats{Speed: math.MaxUint64, InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}},
		{name: "rate overflow", stats: common.InterfaceStats{Speed: maxNetworkObservationRate, InBitsPerSec: math.MaxUint64, InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}},
		{name: "json safe integer overflow", stats: common.InterfaceStats{Speed: maxNetworkObservationRate + 1, InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}},
		{name: "at capacity", stats: common.InterfaceStats{Speed: 1000, InBitsPerSec: 1000, InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}, want: ptr.To(int32(0))},
		{name: "over capacity", stats: common.InterfaceStats{Speed: 1000, OutBitsPerSec: 1001, InRatePresent: true, OutRatePresent: true, InRateValid: true, OutRateValid: true, RateSource: "fixture"}, want: ptr.To(int32(0))},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := interfaceHeadroom(test.stats)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("headroom=%v, want %v", got, test.want)
			}
		})
	}
}

func TestNormalizeNeighborsPreservesOSPFRoutingContext(t *testing.T) {
	neighbors, err := normalizeNeighbors(nil, []common.OSPFNeighbor{{
		NeighborID: "10.0.0.2", Interface: "Gi1/0/1", State: "full", Area: "0", VRF: "blue", ProcessID: "100",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 1 || neighbors[0].RoutingDomain != "vrf=blue,process=100,area=0" {
		t.Fatalf("OSPF routing context = %#v", neighbors)
	}
}

func TestNormalizeNeighborsKeepsDelimiterBearingAdjacenciesDistinct(t *testing.T) {
	neighbors, err := normalizeNeighbors([]common.CDPNeighbor{
		{DeviceID: "peer|one", LocalInterface: "Gi1"},
		{DeviceID: "peer", LocalInterface: "one|Gi1"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 || neighbors[0].Identity == neighbors[1].Identity {
		t.Fatalf("delimiter-bearing identities collided: %#v", neighbors)
	}
	if !strings.HasPrefix(neighbors[0].Identity, "sha256:") || !strings.HasPrefix(neighbors[1].Identity, "sha256:") {
		t.Fatalf("delimiter-bearing identities must be canonical hashes: %#v", neighbors)
	}
	if got := ospfRoutingDomain(common.OSPFNeighbor{VRF: "blue,edge", ProcessID: "100=active", Area: "0"}); got != "vrf=blue%2Cedge,process=100%3Dactive,area=0" {
		t.Fatalf("escaped OSPF routing domain = %q", got)
	}
}

func TestNormalizeNeighborsPreservesBoundedCDPRemoteInterface(t *testing.T) {
	neighbors, err := normalizeNeighbors([]common.CDPNeighbor{{
		DeviceID: "peer-a", LocalInterface: "GigabitEthernet1/0/1", RemoteInterface: "GigabitEthernet1/0/24",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 1 || neighbors[0].RemoteInterface != "GigabitEthernet1/0/24" {
		t.Fatalf("CDP remote interface = %#v", neighbors)
	}
	if _, err := normalizeNeighbors([]common.CDPNeighbor{{
		DeviceID: "peer-a", LocalInterface: "Gi1", RemoteInterface: strings.Repeat("r", 129),
	}}, nil); err == nil {
		t.Fatal("oversized CDP remote interface was accepted")
	}
}

func TestBuildNetworkObservationFailsClosedOnOversizedStatusFields(t *testing.T) {
	observation, err := BuildNetworkObservation(context.Background(), observationTopologyProvider{
		ospf: []common.OSPFNeighbor{{NeighborID: strings.Repeat("n", 129), State: "full"}},
	}, "serial-01", "sha256:worker", "pod-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || observation.UnknownReason != "neighbors: field length invalid" {
		t.Fatalf("oversized neighbor evidence = %#v", observation)
	}
	observation, err = BuildNetworkObservation(context.Background(), observationTopologyProvider{
		interfaces: []common.InterfaceStats{{Name: strings.Repeat("i", 129)}},
	}, "serial-01", "sha256:worker", "pod-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if observation.Complete || observation.UnknownReason != "interfaces: field length invalid" {
		t.Fatalf("oversized interface evidence = %#v", observation)
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
	newer.CollectionStartedAt = metav1.NewTime(newer.CollectionStartedAt.Add(time.Second))
	newer.CollectionEndedAt = metav1.NewTime(newer.CollectionEndedAt.Add(time.Second))
	newer.ObservedAt = metav1.NewTime(newer.ObservedAt.Add(time.Second))
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
		{"observed before collection", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.ObservedAt = metav1.NewTime(o.CollectionStartedAt.Add(-time.Nanosecond))
		}, "outside its collection interval"},
		{"observed after collection", func(_ *ciskov1.CiscoDevice, o *ciskov1.DeviceNetworkObservationStatus) {
			o.ObservedAt = metav1.NewTime(o.CollectionEndedAt.Add(time.Nanosecond))
		}, "outside its collection interval"},
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
			previous.CollectionStartedAt = metav1.NewTime(previous.CollectionStartedAt.Add(-time.Second))
			previous.CollectionEndedAt = metav1.NewTime(previous.CollectionEndedAt.Add(-time.Second))
			previous.ObservedAt = metav1.NewTime(previous.ObservedAt.Add(-time.Second))
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
	o.CollectionStartedAt = metav1.NewTime(o.CollectionStartedAt.Add(time.Second))
	o.CollectionEndedAt = metav1.NewTime(o.CollectionEndedAt.Add(time.Second))
	o.ObservedAt = metav1.NewTime(o.ObservedAt.Add(time.Second))
	c := newTopologyObservationClient(t, d)
	if err := PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), d.UID, o); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("sequence exhaustion error = %v", err)
	}
}

func TestPublishNetworkObservationRejectsOlderCollection(t *testing.T) {
	d, o := topologyObservationFixture()
	accepted := o.DeepCopy()
	accepted.SampleSequence = 10
	d.Status.HealthObservation.Network = accepted
	older := o.DeepCopy()
	older.SampleSequence = 0
	older.CollectionStartedAt = metav1.NewTime(accepted.CollectionStartedAt.Add(-2 * time.Minute))
	older.CollectionEndedAt = metav1.NewTime(accepted.CollectionEndedAt.Add(-time.Minute))
	older.ObservedAt = older.CollectionStartedAt
	c := newTopologyObservationClient(t, d)
	err := PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), d.UID, older)
	if err == nil || !strings.Contains(err.Error(), "not newer than accepted") {
		t.Fatalf("older collection error = %v", err)
	}
	var got ciskov1.CiscoDevice
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(d), &got); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(got.Status.HealthObservation.Network, accepted) {
		t.Fatalf("older collection changed accepted observation: %#v", got.Status.HealthObservation.Network)
	}
}

func TestPublishNetworkObservationConcurrentWritersPreserveOneHighWaterMark(t *testing.T) {
	d, first := topologyObservationFixture()
	base := newTopologyObservationClient(t, d)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	c := &observationStatusClient{Client: base, writer: &barrierObservationStatusWriter{
		delegate: base.Status(), arrived: arrived, release: release,
	}}
	second := first.DeepCopy()
	second.CollectionStartedAt = metav1.NewTime(second.CollectionStartedAt.Add(time.Second))
	second.CollectionEndedAt = metav1.NewTime(second.CollectionEndedAt.Add(time.Second))
	second.ObservedAt = metav1.NewTime(second.ObservedAt.Add(time.Second))

	results := make(chan error, 2)
	for _, sample := range []*ciskov1.DeviceNetworkObservationStatus{first, second} {
		go func(sample *ciskov1.DeviceNetworkObservationStatus) {
			results <- PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), d.UID, sample)
		}(sample)
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			t.Fatal("concurrent publishers did not reach the status boundary")
		}
	}
	close(release)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case apierrors.IsConflict(err):
			conflicts++
		default:
			t.Fatalf("concurrent publication error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results successes=%d conflicts=%d", successes, conflicts)
	}
	var persisted ciskov1.CiscoDevice
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(d), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status.HealthObservation == nil || persisted.Status.HealthObservation.Network == nil ||
		persisted.Status.HealthObservation.Network.SampleSequence != 1 {
		t.Fatalf("persisted high-water mark = %#v", persisted.Status.HealthObservation)
	}
	third := first.DeepCopy()
	third.SampleSequence = 0
	third.CollectionStartedAt = metav1.NewTime(second.CollectionStartedAt.Add(time.Second))
	third.CollectionEndedAt = metav1.NewTime(second.CollectionEndedAt.Add(time.Second))
	third.ObservedAt = metav1.NewTime(second.ObservedAt.Add(time.Second))
	if err := PublishNetworkObservation(context.Background(), base, client.ObjectKeyFromObject(d), d.UID, third); err != nil {
		t.Fatalf("fresh sample after conflict rejected: %v", err)
	}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(d), &persisted); err != nil {
		t.Fatal(err)
	}
	if got := persisted.Status.HealthObservation.Network.SampleSequence; got != 2 {
		t.Fatalf("post-conflict sequence=%d, want 2", got)
	}
}

func TestPublishNetworkObservationLostResponseDoesNotReplay(t *testing.T) {
	d, sample := topologyObservationFixture()
	base := newTopologyObservationClient(t, d)
	writer := &lostResponseObservationStatusWriter{delegate: base.Status()}
	c := &observationStatusClient{Client: base, writer: writer}
	err := PublishNetworkObservation(context.Background(), c, client.ObjectKeyFromObject(d), d.UID, sample)
	if err == nil || !strings.Contains(err.Error(), "simulated lost status response") {
		t.Fatalf("lost response error = %v", err)
	}
	var persisted ciskov1.CiscoDevice
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(d), &persisted); err != nil {
		t.Fatal(err)
	}
	if got := persisted.Status.HealthObservation.Network.SampleSequence; got != 1 {
		t.Fatalf("persisted lost-response sequence=%d, want 1", got)
	}
	if err := PublishNetworkObservation(context.Background(), base, client.ObjectKeyFromObject(d), d.UID, sample); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("replayed lost-response sample error = %v", err)
	}
	newer := sample.DeepCopy()
	newer.SampleSequence = 0
	newer.CollectionStartedAt = metav1.NewTime(newer.CollectionStartedAt.Add(time.Second))
	newer.CollectionEndedAt = metav1.NewTime(newer.CollectionEndedAt.Add(time.Second))
	newer.ObservedAt = metav1.NewTime(newer.ObservedAt.Add(time.Second))
	if err := PublishNetworkObservation(context.Background(), base, client.ObjectKeyFromObject(d), d.UID, newer); err != nil {
		t.Fatalf("fresh sample after lost response rejected: %v", err)
	}
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(d), &persisted); err != nil {
		t.Fatal(err)
	}
	if got := persisted.Status.HealthObservation.Network.SampleSequence; got != 2 {
		t.Fatalf("post-loss sequence=%d, want 2", got)
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
		Interfaces: []ciskov1.DeviceNetworkInterfaceObservation{{
			Name: "Gi1", OperUp: true, HeadroomPercent: ptr.To(int32(90)),
			CapacityBitsPerSecond: ptr.To(uint64(1000)), IngressBitsPerSecond: ptr.To(uint64(100)),
			EgressBitsPerSecond: ptr.To(uint64(50)), RateSource: "fixture",
		}},
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
