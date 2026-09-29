package provider

import (
	"context"
	"errors"
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
	}, "SERIAL-01", "sha256:worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Complete || observation.DeviceIdentityHash == "" {
		t.Fatalf("observation=%#v", observation)
	}
	if got := observation.Interfaces[0].Name; got != "Gi1" {
		t.Fatalf("interfaces not sorted: %#v", observation.Interfaces)
	}
	if len(observation.Neighbors) != 2 || observation.Neighbors[0].Source != "cdp,ospf" {
		t.Fatalf("neighbors not merged: %#v", observation.Neighbors)
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
