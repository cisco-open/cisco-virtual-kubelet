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
	"math"
	"net/url"
	"sort"
	"strconv"
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
	maxNetworkObservationInterfaces    = 64
	maxNetworkObservationNeighbors     = 64
	maxNetworkObservationUnknownReason = 256
	networkObservationInterval         = 30 * time.Second
	networkObservationTimeout          = 20 * time.Second
	networkObservationPublishTimeout   = 5 * time.Second
)

type networkObservationCollectionResult struct {
	observation *ciskov1.DeviceNetworkObservationStatus
	err         error
}

// networkObservationCollector permits at most one provider collection at a
// time. A driver that ignores context cannot be force-stopped by Go; after its
// deadline we publish incomplete evidence and refuse to start another
// collector until the original call returns. This bounds leaked work to one
// call per network worker and prevents a hung device source from creating an
// unbounded goroutine backlog.
type networkObservationCollector struct {
	result  <-chan networkObservationCollectionResult
	started time.Time
	expired bool
}

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
	workerPodUID ...string,
) {
	collector := &networkObservationCollector{}
	publish := func() {
		collectionCtx, collectionCancel := context.WithTimeout(ctx, networkObservationTimeout)
		observation, err := collector.collect(collectionCtx, provider, physicalIdentity, producerRevision, time.Now(), workerPodUID...)
		collectionCancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.G(ctx).WithError(err).Warn("network topology observation failed")
			return
		}
		publishCtx, publishCancel := context.WithTimeout(ctx, networkObservationPublishTimeout)
		err = publishNetworkObservation(publishCtx, c, deviceKey, deviceUID, observation)
		publishCancel()
		if err != nil {
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

func (c *networkObservationCollector) collect(
	ctx context.Context,
	provider drivers.TopologyProvider,
	physicalIdentity string,
	producerRevision string,
	now time.Time,
	workerPodUID ...string,
) (*ciskov1.DeviceNetworkObservationStatus, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if c.result != nil {
		select {
		case result := <-c.result:
			c.result = nil
			if !c.expired {
				return result.observation, result.err
			}
			// The result returned after its deadline is stale by definition.
			// Drop it and start a fresh collection below.
			c.expired = false
		default:
			// Keep the original collection timestamp while it remains stuck. A
			// periodic incomplete status must not look like newly collected,
			// fresh evidence merely because the publisher ticked again.
			return incompleteNetworkObservation(physicalIdentity, producerRevision, c.started, c.started,
				"collection remains in flight after deadline", workerPodUID...)
		}
	}
	result := make(chan networkObservationCollectionResult, 1)
	c.result = result
	c.started = now.UTC()
	go func() {
		observation, err := BuildNetworkObservation(ctx, provider, physicalIdentity, producerRevision, now, workerPodUID...)
		result <- networkObservationCollectionResult{observation: observation, err: err}
	}()
	select {
	case result := <-c.result:
		c.result = nil
		return result.observation, result.err
	case <-ctx.Done():
		c.expired = true
		return incompleteNetworkObservation(physicalIdentity, producerRevision, c.started, now,
			"collection deadline exceeded", workerPodUID...)
	}
}

func incompleteNetworkObservation(
	physicalIdentity string,
	producerRevision string,
	started, now time.Time,
	reason string,
	workerPodUID ...string,
) (*ciskov1.DeviceNetworkObservationStatus, error) {
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
	if started.IsZero() {
		started = now
	}
	status := &ciskov1.DeviceNetworkObservationStatus{
		CollectionStartedAt: metav1.NewTime(started.UTC()), CollectionEndedAt: metav1.NewTime(now.UTC()),
		ObservedAt: metav1.NewTime(now.UTC()), Complete: false,
		UnknownReason: truncateNetworkObservationReason(reason), ProducerRevision: producerRevision,
		DeviceIdentityHash: identityHash(identity),
	}
	if len(workerPodUID) > 0 {
		status.WorkerPodUID = strings.TrimSpace(workerPodUID[0])
		if len(status.WorkerPodUID) > 128 {
			return nil, fmt.Errorf("worker Pod UID exceeds 128 characters")
		}
	}
	return status, nil
}

// publishNetworkObservation deliberately does not retry a conflict.
// A collection has a fixed time interval: assigning it a new sequence after a
// competing write could make an older measurement supersede a newer one. The
// next publisher interval collects a new snapshot instead.
func publishNetworkObservation(
	ctx context.Context,
	c client.Client,
	deviceKey types.NamespacedName,
	deviceUID types.UID,
	observation *ciskov1.DeviceNetworkObservationStatus,
) error {
	return PublishNetworkObservation(ctx, c, deviceKey, deviceUID, observation)
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
	workerPodUID ...string,
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
	// Preserve the earliest time at which this sample could have reflected
	// device state. A slow collection or delayed status publication must not
	// refresh the freshness window for data collected earlier.
	collectionStarted := now.UTC()

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
	if len(workerPodUID) > 0 {
		status.WorkerPodUID = strings.TrimSpace(workerPodUID[0])
		if len(status.WorkerPodUID) > 128 {
			return nil, fmt.Errorf("worker Pod UID exceeds 128 characters")
		}
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
		normalizationFailures = append(normalizationFailures, boundedNormalizationReason("interfaces", err))
	}
	status.Neighbors, err = normalizeNeighbors(cdp, ospf)
	if err != nil {
		normalizationFailures = append(normalizationFailures, boundedNormalizationReason("neighbors", err))
	}
	if len(normalizationFailures) != 0 {
		status.Complete = false
		if status.UnknownReason != "" {
			status.UnknownReason += "; "
		}
		status.UnknownReason += strings.Join(normalizationFailures, "; ")
	}
	status.UnknownReason = truncateNetworkObservationReason(status.UnknownReason)
	status.CollectionEndedAt = metav1.NewTime(time.Now().UTC())
	return status, nil
}

// boundedNormalizationReason keeps untrusted interface and neighbor names out
// of status while preserving an operator-useful reason class. The CRD caps this
// field at 256 bytes, so truncation is a final defensive bound as well.
func boundedNormalizationReason(source string, err error) string {
	reason := strings.ToLower(err.Error())
	switch {
	case strings.Contains(reason, "duplicate"):
		return source + ": duplicate identity"
	case strings.Contains(reason, "unnamed"):
		return source + ": unnamed identity"
	case strings.Contains(reason, "limit"):
		return source + ": observation limit exceeded"
	case strings.Contains(reason, "exceeds maximum length"):
		return source + ": field length invalid"
	default:
		return source + ": normalization failed"
	}
}

func truncateNetworkObservationReason(reason string) string {
	if len(reason) <= maxNetworkObservationUnknownReason {
		return reason
	}
	return reason[:maxNetworkObservationUnknownReason]
}

// PublishNetworkObservation patches only CiscoDevice.status.healthObservation.network.
// It verifies the device UID, manager Node binding, exact network-worker
// revision/Pod binding, and physical identity before writing, so a worker from
// a replaced device incarnation cannot publish into its successor. Accepted
// samples from the same worker incarnation must advance monotonically.
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
	networkWorker := device.Status.NetworkWorkerRevision
	if networkWorker == nil {
		return fmt.Errorf("network worker binding is absent")
	}
	if networkWorker.DesiredRevision == "" || networkWorker.ObservedRevision == "" ||
		networkWorker.DesiredRevision != networkWorker.ObservedRevision {
		return fmt.Errorf("network worker binding is not at the desired revision")
	}
	if networkWorker.PodUID == "" || networkWorker.PodStartTime == nil || networkWorker.PodReadyTime == nil ||
		networkWorker.PodReadyTime.Before(networkWorker.PodStartTime) {
		return fmt.Errorf("network worker binding has no valid ready Pod identity")
	}
	if observation.ProducerRevision != networkWorker.ObservedRevision {
		return fmt.Errorf("observation producer revision %q does not match bound network worker revision %q",
			observation.ProducerRevision, networkWorker.ObservedRevision)
	}
	if observation.WorkerPodUID == "" || observation.WorkerPodUID != networkWorker.PodUID {
		return fmt.Errorf("observation worker Pod UID %q does not match bound network worker Pod %q",
			observation.WorkerPodUID, networkWorker.PodUID)
	}
	if observation.CollectionStartedAt.IsZero() || observation.CollectionEndedAt.IsZero() {
		return fmt.Errorf("observation is missing collection provenance")
	}
	if observation.CollectionEndedAt.Before(&observation.CollectionStartedAt) {
		return fmt.Errorf("observation collection interval is invalid")
	}
	if observation.ObservedAt.IsZero() || observation.ObservedAt.Before(&observation.CollectionStartedAt) ||
		observation.ObservedAt.After(observation.CollectionEndedAt.Time) {
		return fmt.Errorf("observation time is outside its collection interval")
	}
	physicalIdentity, err := topology.CanonicalPhysicalIdentity(device.Status.NodeIdentity.PhysicalIdentity)
	if err != nil {
		return fmt.Errorf("manager physical identity is invalid: %w", err)
	}
	if observation.DeviceIdentityHash != identityHash(physicalIdentity) {
		return fmt.Errorf("observation device identity does not match manager binding")
	}
	current := currentNetworkObservation(device.Status.HealthObservation)
	if current != nil && !observation.CollectionEndedAt.After(current.CollectionEndedAt.Time) {
		return fmt.Errorf("observation collection ended at %s is not newer than accepted sample at %s",
			observation.CollectionEndedAt.UTC().Format(time.RFC3339Nano),
			current.CollectionEndedAt.UTC().Format(time.RFC3339Nano))
	}
	if observation.SampleSequence == 0 {
		sequence, err := nextNetworkObservationSequence(device.Status.HealthObservation, observation.ProducerRevision, observation.WorkerPodUID)
		if err != nil {
			return err
		}
		observation = observation.DeepCopy()
		observation.SampleSequence = sequence
	}
	if current != nil && current.ProducerRevision == observation.ProducerRevision &&
		current.WorkerPodUID == observation.WorkerPodUID && current.SampleSequence >= observation.SampleSequence {
		return fmt.Errorf("observation sample sequence %d is not newer than accepted sequence %d",
			observation.SampleSequence, current.SampleSequence)
	}
	before := device.DeepCopy()
	if device.Status.HealthObservation == nil {
		device.Status.HealthObservation = &ciskov1.DeviceHealthObservationStatus{}
	}
	device.Status.HealthObservation.Network = observation.DeepCopy()
	return c.Status().Patch(ctx, &device, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func currentNetworkObservation(health *ciskov1.DeviceHealthObservationStatus) *ciskov1.DeviceNetworkObservationStatus {
	if health == nil {
		return nil
	}
	return health.Network
}

func nextNetworkObservationSequence(
	health *ciskov1.DeviceHealthObservationStatus,
	producerRevision string,
	workerPodUID string,
) (uint64, error) {
	if health == nil || health.Network == nil ||
		health.Network.ProducerRevision != producerRevision ||
		health.Network.WorkerPodUID != workerPodUID {
		return 1, nil
	}
	if health.Network.SampleSequence == math.MaxUint64 {
		return 0, fmt.Errorf("observation sample sequence exhausted")
	}
	return health.Network.SampleSequence + 1, nil
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
		if err := requireNetworkObservationFieldLength("interface name", name, 128); err != nil {
			return nil, err
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
		if err := requireNetworkObservationFieldLength("neighbor ID", id, 128); err != nil {
			return nil, err
		}
		if err := requireNetworkObservationFieldLength("neighbor interface", localInterface, 128); err != nil {
			return nil, err
		}
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
		routingDomain := ospfRoutingDomain(value)
		if err := requireNetworkObservationFieldLength("neighbor ID", id, 128); err != nil {
			return nil, err
		}
		if err := requireNetworkObservationFieldLength("neighbor interface", localInterface, 128); err != nil {
			return nil, err
		}
		if err := requireNetworkObservationFieldLength("OSPF routing domain", routingDomain, 64); err != nil {
			return nil, err
		}
		state := strings.TrimSpace(value.State)
		if err := requireNetworkObservationFieldLength("OSPF state", state, 32); err != nil {
			return nil, err
		}
		identity := neighborIdentity("ospf", id, localInterface, routingDomain)
		if _, exists := byIdentity[identity]; exists {
			return nil, fmt.Errorf("source returned duplicate OSPF adjacency %q", identity)
		}
		byIdentity[identity] = ciskov1.DeviceNetworkNeighborObservation{
			Identity: identity, ID: id, Interface: localInterface, RoutingDomain: routingDomain,
			State: state, Source: "ospf",
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

func requireNetworkObservationFieldLength(name, value string, maximum int) error {
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds maximum length %d", name, maximum)
	}
	return nil
}

// ospfRoutingDomain preserves the actual routing-instance and process context
// when the driver supplies it. Area alone is retained only as a legacy
// fallback; it is not treated as a VRF identity.
func ospfRoutingDomain(value common.OSPFNeighbor) string {
	parts := make([]string, 0, 3)
	if vrf := strings.TrimSpace(value.VRF); vrf != "" {
		parts = append(parts, "vrf="+url.QueryEscape(vrf))
	}
	if processID := strings.TrimSpace(value.ProcessID); processID != "" {
		parts = append(parts, "process="+url.QueryEscape(processID))
	}
	if area := strings.TrimSpace(value.Area); area != "" {
		parts = append(parts, "area="+url.QueryEscape(area))
	}
	return strings.Join(parts, ",")
}

func neighborIdentity(source, id, localInterface, routingDomain string) string {
	values := []string{source, id, localInterface, routingDomain}
	legacy := strings.Join(values, "|")
	// Keep existing readable identities when every component is unambiguous.
	// A delimiter-bearing component instead uses a length-prefixed canonical
	// form before hashing, so distinct adjacencies cannot collide by shifting a
	// delimiter across fields.
	hasDelimiter := false
	for _, value := range values {
		hasDelimiter = hasDelimiter || strings.Contains(value, "|")
	}
	if !hasDelimiter && len(legacy) <= 128 {
		return legacy
	}
	var canonical strings.Builder
	for _, value := range values {
		canonical.WriteString(strconv.Itoa(len(value)))
		canonical.WriteByte(':')
		canonical.WriteString(value)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// interfaceHeadroom derives a conservative percentage from the driver's
// directional counters. Speed and rates are expressed in bits per second by
// the common driver contract. Missing/zero capacity or counters outside the
// representable range remain Unknown (nil), never zero-headroom.
func interfaceHeadroom(value common.InterfaceStats) *int32 {
	if value.Speed == 0 || !value.InRatePresent || !value.OutRatePresent ||
		!value.InRateValid || !value.OutRateValid {
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
