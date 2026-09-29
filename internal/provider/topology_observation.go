// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
package provider

// This file contains the small, driver-neutral bridge used by a network
// worker to publish bounded topology evidence.  Drivers remain responsible
// for collecting and authenticating device data; this bridge only normalizes
// it, applies size limits, and writes the network-owned status field.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxNetworkObservationInterfaces = 64
	maxNetworkObservationNeighbors  = 64
	networkObservationInterval      = 30 * time.Second
)

// RunNetworkObservationPublisher keeps the manager-owned summary fresh from
// the network worker. Failed samples are published as incomplete evidence by
// the next successful collection; a failed write is logged and retried at the
// next interval without widening any device authority.
func RunNetworkObservationPublisher(
	ctx context.Context,
	c client.Client,
	deviceKey types.NamespacedName,
	deviceUID types.UID,
	physicalIdentity string,
	producerRevision string,
	provider drivers.TopologyProvider,
) {
	publish := func() {
		observation, err := BuildNetworkObservation(ctx, provider, physicalIdentity, producerRevision, time.Now())
		if err != nil {
			log.G(ctx).WithError(err).Warn("network topology observation failed")
			return
		}
		if err := PublishNetworkObservation(ctx, c, deviceKey, deviceUID, observation); err != nil {
			log.G(ctx).WithError(err).Warn("network topology observation status update failed")
		}
	}
	publish()
	ticker := time.NewTicker(networkObservationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

// BuildNetworkObservation turns a driver snapshot into a bounded status
// value. Any failed source makes the snapshot incomplete; an empty successful
// source is still complete and is deliberately not treated as healthy by the
// rollout gate unless policy explicitly permits it.
func BuildNetworkObservation(
	ctx context.Context,
	provider drivers.TopologyProvider,
	physicalIdentity string,
	producerRevision string,
	now time.Time,
) (*ciskov1.DeviceNetworkObservationStatus, error) {
	if provider == nil {
		return nil, fmt.Errorf("topology provider is nil")
	}
	identity, err := topology.CanonicalPhysicalIdentity(physicalIdentity)
	if err != nil {
		return nil, fmt.Errorf("canonical physical identity: %w", err)
	}
	if strings.TrimSpace(producerRevision) == "" {
		return nil, fmt.Errorf("producer revision is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	interfaces, interfaceErr := provider.GetInterfaceStats(ctx)
	cdp, cdpErr := provider.GetCDPNeighbors(ctx)
	ospf, ospfErr := provider.GetOSPFNeighbors(ctx)

	status := &ciskov1.DeviceNetworkObservationStatus{
		ObservedAt:         metav1.NewTime(now.UTC()),
		Complete:           interfaceErr == nil && cdpErr == nil && ospfErr == nil,
		ProducerRevision:   producerRevision,
		DeviceIdentityHash: identityHash(identity),
	}
	var failures []string
	if interfaceErr != nil {
		failures = append(failures, "interfaces")
	}
	if cdpErr != nil {
		failures = append(failures, "cdp")
	}
	if ospfErr != nil {
		failures = append(failures, "ospf")
	}
	if len(failures) != 0 {
		status.UnknownReason = "unavailable sources: " + strings.Join(failures, ",")
	}

	status.Interfaces = normalizeInterfaces(interfaces)
	status.Neighbors = normalizeNeighbors(cdp, ospf)
	return status, nil
}

// PublishNetworkObservation patches only CiscoDevice.status.healthObservation.network.
// It verifies the device UID and manager Node binding before writing, so a
// worker from a replaced device incarnation cannot publish into its successor.
func PublishNetworkObservation(
	ctx context.Context,
	c client.Client,
	deviceKey types.NamespacedName,
	deviceUID types.UID,
	observation *ciskov1.DeviceNetworkObservationStatus,
) error {
	if c == nil || observation == nil {
		return fmt.Errorf("client and observation are required")
	}
	var device ciskov1.CiscoDevice
	if err := c.Get(ctx, deviceKey, &device); err != nil {
		return err
	}
	if deviceUID != "" && device.UID != deviceUID {
		return fmt.Errorf("device UID changed from %q to %q", deviceUID, device.UID)
	}
	if device.Status.NodeIdentity == nil || device.Status.NodeIdentity.DeviceUID != string(device.UID) {
		return fmt.Errorf("device is not manager-bound to a Node")
	}
	before := device.DeepCopy()
	if device.Status.HealthObservation == nil {
		device.Status.HealthObservation = &ciskov1.DeviceHealthObservationStatus{}
	}
	device.Status.HealthObservation.Network = observation.DeepCopy()
	return c.Status().Patch(ctx, &device, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func identityHash(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeInterfaces(values []common.InterfaceStats) []ciskov1.DeviceNetworkInterfaceObservation {
	byName := make(map[string]ciskov1.DeviceNetworkInterfaceObservation, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" || len(byName) >= maxNetworkObservationInterfaces {
			continue
		}
		byName[name] = ciskov1.DeviceNetworkInterfaceObservation{
			Name: name, OperUp: strings.EqualFold(strings.TrimSpace(value.OperStatus), "up"),
		}
	}
	out := make([]ciskov1.DeviceNetworkInterfaceObservation, 0, len(byName))
	for _, value := range byName {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeNeighbors(cdp []common.CDPNeighbor, ospf []common.OSPFNeighbor) []ciskov1.DeviceNetworkNeighborObservation {
	byID := make(map[string]ciskov1.DeviceNetworkNeighborObservation, len(cdp)+len(ospf))
	for _, value := range cdp {
		id := strings.TrimSpace(value.DeviceID)
		if id == "" || len(byID) >= maxNetworkObservationNeighbors {
			continue
		}
		byID[id] = ciskov1.DeviceNetworkNeighborObservation{ID: id, State: "discovered", Source: "cdp"}
	}
	for _, value := range ospf {
		id := strings.TrimSpace(value.NeighborID)
		if id == "" {
			continue
		}
		if existing, ok := byID[id]; ok {
			existing.State = strings.TrimSpace(value.State)
			existing.Source = "cdp,ospf"
			byID[id] = existing
			continue
		}
		if len(byID) >= maxNetworkObservationNeighbors {
			continue
		}
		byID[id] = ciskov1.DeviceNetworkNeighborObservation{ID: id, State: strings.TrimSpace(value.State), Source: "ospf"}
	}
	out := make([]ciskov1.DeviceNetworkNeighborObservation, 0, len(byID))
	for _, value := range byID {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
