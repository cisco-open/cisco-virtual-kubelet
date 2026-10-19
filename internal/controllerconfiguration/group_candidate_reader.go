// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadGroupReservationCandidates collects a complete exclusive target snapshot
// using an uncached cluster-wide reader. It performs no API writes. Installed
// protocol/admission compatibility and dispatch revalidation remain separate.
func ReadGroupReservationCandidates(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	return readGroupReservationCandidates(ctx, reader, expected, now, false)
}

// ReadCancelledGroupReservationCandidates constructs missing recovery records
// only under a durable cancellation fence. Current identities and source binding
// remain mandatory; these records can never authorize Prepared submission.
func ReadCancelledGroupReservationCandidates(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	return readGroupReservationCandidates(ctx, reader, expected, now, true)
}

func readGroupReservationCandidates(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time, cancelling bool) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("configuration group changed or lacks exclusive current managed targets")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || now.IsZero() {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Spec.Type == "" || nc.Status.Configuration == nil {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	if err := ValidateGroupJournalIdentity(op); err != nil {
		return nil, err
	}
	targets, err := CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	var all cvk.CiscoDeviceList
	if err := reader.List(ctx, &all); err != nil {
		return nil, err
	}
	devices, err := exclusiveGroupDevices(nc.Namespace, targets, all.Items)
	if err != nil {
		return nil, err
	}
	observed := []client.Object{&nc}
	members := make([]GroupMemberSnapshot, 0, len(targets))
	for i, target := range targets {
		device := devices[i]
		var node core.Node
		if err := reader.Get(ctx, client.ObjectKey{Name: target.Node.Name}, &node); err != nil {
			return nil, err
		}
		name := node.Annotations[managedprotocol.AnnotationNetworkWorkerPodName]
		if name == "" {
			return nil, reject
		}
		var worker core.Pod
		if err := reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: name}, &worker); err != nil {
			return nil, err
		}
		if string(worker.UID) != node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] {
			return nil, reject
		}
		members = append(members, GroupMemberSnapshot{device, &node, &worker})
		observed = append(observed, device, &node, &worker)
	}
	deployment, connector, err := readCandidateConnector(ctx, reader, &nc)
	if err != nil {
		return nil, err
	}
	candidates, err := groupReservationCandidates(&nc, members, connector, now, cancelling)
	if err != nil {
		return nil, err
	}
	// All candidates share one controller, intent and connector identity.
	if err := readBoundConfigurationIntent(ctx, reader, &nc, candidates[0]); err != nil {
		return nil, err
	}
	if err := ReadControllerWorkerBinding(ctx, reader, candidates[0], now); err != nil {
		return nil, err
	}
	observed = append(observed, deployment, connector)
	// A second global list catches aliases introduced during dependency reads.
	var late cvk.CiscoDeviceList
	if err := reader.List(ctx, &late); err != nil {
		return nil, err
	}
	current, err := exclusiveGroupDevices(nc.Namespace, targets, late.Items)
	if err != nil {
		return nil, err
	}
	for i := range current {
		if current[i].ResourceVersion != devices[i].ResourceVersion {
			return nil, reject
		}
	}
	for _, original := range observed {
		fresh := original.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(original), fresh); err != nil {
			return nil, err
		}
		if fresh.GetUID() != original.GetUID() || fresh.GetResourceVersion() != original.GetResourceVersion() {
			return nil, reject
		}
	}
	return candidates, nil
}

func exclusiveGroupDevices(namespace string, targets []GroupTargetIdentity, devices []cvk.CiscoDevice) ([]*cvk.CiscoDevice, error) {
	reject := errors.New("configuration group physical declaration is missing or ambiguous")
	wanted := make(map[string]bool, len(targets))
	for _, target := range targets {
		wanted[target.PhysicalIdentity] = true
	}
	found := make(map[string]*cvk.CiscoDevice, len(targets))
	for i := range devices {
		d := &devices[i]
		physical, err := topologyidentity.CanonicalPhysicalIdentity(d.Spec.PhysicalIdentity)
		if err != nil || !wanted[physical] {
			continue
		}
		if found[physical] != nil {
			return nil, reject
		}
		found[physical] = d
	}
	result := make([]*cvk.CiscoDevice, 0, len(targets))
	for _, target := range targets {
		d := found[target.PhysicalIdentity]
		if d == nil || d.Namespace != namespace || d.Name != target.Device.Name || string(d.UID) != target.Device.UID || d.ResourceVersion == "" {
			return nil, reject
		}
		result = append(result, d)
	}
	return result, nil
}
