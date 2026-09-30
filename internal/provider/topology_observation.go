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
	var sampleSequence uint64
	publish := func() {
		observation, err := BuildNetworkObservation(ctx, provider, physicalIdentity, producerRevision, time.Now())
		if err != nil {
			log.G(ctx).WithError(err).Warn("network topology observation failed")
			return
		}
		sampleSequence++
		observation.SampleSequence = sampleSequence
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
	collectionStarted := time.Now().UTC()
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
		CollectionStartedAt: metav1.NewTime(collectionStarted),
		ObservedAt:          metav1.NewTime(now.UTC()),
		Complete:            interfaceErr == nil && cdpErr == nil && ospfErr == nil,
		ProducerRevision:    producerRevision,
		DeviceIdentityHash:  identityHash(identity),
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

	var normalizationFailures []string
	status.Interfaces, err = normalizeInterfaces(interfaces)
	if err != nil {
		normalizationFailures = append(normalizationFailures, "interfaces: "+err.Error())
	}
	status.Neighbors, err = normalizeNeighbors(cdp, ospf)
	if err != nil {
		normalizationFailures = append(normalizationFailures, "neighbors: "+err.Error())
	}
	if len(normalizationFailures) != 0 {
		status.Complete = false
		if status.UnknownReason != "" {
			status.UnknownReason += "; "
		}
		status.UnknownReason += strings.Join(normalizationFailures, "; ")
	}
	status.CollectionEndedAt = metav1.NewTime(time.Now().UTC())
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

func normalizeInterfaces(values []common.InterfaceStats) ([]ciskov1.DeviceNetworkInterfaceObservation, error) {
	if len(values) > maxNetworkObservationInterfaces {
		return nil, fmt.Errorf("source returned %d interfaces; limit is %d", len(values), maxNetworkObservationInterfaces)
	}
	byName := make(map[string]ciskov1.DeviceNetworkInterfaceObservation, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			return nil, fmt.Errorf("source returned an unnamed interface")
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("source returned duplicate interface %q", name)
		}
		headroom := interfaceHeadroom(value)
		byName[name] = ciskov1.DeviceNetworkInterfaceObservation{
			Name: name, OperUp: strings.EqualFold(strings.TrimSpace(value.OperStatus), "up"), HeadroomPercent: headroom,
		}
	}
	out := make([]ciskov1.DeviceNetworkInterfaceObservation, 0, len(byName))
	for _, value := range byName {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func normalizeNeighbors(cdp []common.CDPNeighbor, ospf []common.OSPFNeighbor) ([]ciskov1.DeviceNetworkNeighborObservation, error) {
	if len(cdp)+len(ospf) > maxNetworkObservationNeighbors {
		return nil, fmt.Errorf("sources returned %d neighbors; limit is %d", len(cdp)+len(ospf), maxNetworkObservationNeighbors)
	}
	byIdentity := make(map[string]ciskov1.DeviceNetworkNeighborObservation, len(cdp)+len(ospf))
	for _, value := range cdp {
		id := strings.TrimSpace(value.DeviceID)
		if id == "" {
			return nil, fmt.Errorf("source returned an unnamed CDP neighbor")
		}
		localInterface := strings.TrimSpace(value.LocalInterface)
		identity := neighborIdentity("cdp", id, localInterface, "")
		if _, exists := byIdentity[identity]; exists {
			return nil, fmt.Errorf("source returned duplicate CDP adjacency %q", identity)
		}
		byIdentity[identity] = ciskov1.DeviceNetworkNeighborObservation{
			Identity: identity, ID: id, Interface: localInterface, State: "discovered", Source: "cdp",
		}
	}
	for _, value := range ospf {
		id := strings.TrimSpace(value.NeighborID)
		if id == "" {
			return nil, fmt.Errorf("source returned an unnamed OSPF neighbor")
		}
		localInterface := strings.TrimSpace(value.Interface)
		routingDomain := strings.TrimSpace(value.Area)
		identity := neighborIdentity("ospf", id, localInterface, routingDomain)
		if _, exists := byIdentity[identity]; exists {
			return nil, fmt.Errorf("source returned duplicate OSPF adjacency %q", identity)
		}
		byIdentity[identity] = ciskov1.DeviceNetworkNeighborObservation{
			Identity: identity, ID: id, Interface: localInterface, RoutingDomain: routingDomain,
			State: strings.TrimSpace(value.State), Source: "ospf",
		}
	}
	out := make([]ciskov1.DeviceNetworkNeighborObservation, 0, len(byIdentity))
	for _, value := range byIdentity {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Identity < out[j].Identity
	})
	return out, nil
}

func neighborIdentity(source, id, localInterface, routingDomain string) string {
	canonical := strings.Join([]string{source, id, localInterface, routingDomain}, "|")
	if len(canonical) <= 128 {
		return canonical
	}
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// interfaceHeadroom derives a conservative percentage from the driver's
// directional counters. Speed and rates are expressed in bits per second by
// the common driver contract. Missing/zero capacity or counters outside the
// representable range remain Unknown (nil), never zero-headroom.
func interfaceHeadroom(value common.InterfaceStats) *int32 {
	if value.Speed == 0 {
		return nil
	}
	utilization := value.InBitsPerSec
	if value.OutBitsPerSec > utilization {
		utilization = value.OutBitsPerSec
	}
	if utilization >= value.Speed {
		zero := int32(0)
		return &zero
	}
	remaining := value.Speed - utilization
	// Use floating-point only for this bounded percentage so multiplying a
	// very large counter cannot overflow uint64 before the division.
	headroom := int32(float64(remaining) * 100 / float64(value.Speed))
	if headroom > 100 {
		headroom = 100
	}
	return &headroom
}
