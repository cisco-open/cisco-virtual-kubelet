package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
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
