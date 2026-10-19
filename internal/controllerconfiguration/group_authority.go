// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadGroupAuthority checks live intent, exclusive physical identity, isolated
// connector/network workers, all scheduling guards, native-preparation freshness
// and empty Kubernetes workloads. Readers must be uncached and cluster-wide.
// It does not establish installed schema/admission or worker compatibility;
// callers must verify those separately before using it in an authorizer.
// Native workload inventory is attested by the bound worker's fresh grant, not
// re-queried from the controller. No mutation or dispatch permit is produced.
func ReadGroupAuthority(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, pod ops.ConfigurationReservationReference, namespace string, now time.Time) error {
	reject := errors.New("configuration group lacks current all-member authority")
	if reader == nil || expected == nil || expected.UID == "" || expected.Status.Configuration == nil || pod.Name == "" || pod.UID == "" || now.IsZero() {
		return reject
	}
	if err := ValidateGroupJournalIdentity(expected.Status.Configuration.Operation); err != nil {
		return err
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return err
	}
	if nc.UID != expected.UID || nc.Generation != expected.Generation || nc.Spec.Type != expected.Spec.Type || nc.Spec.Type == "" || nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Status.Configuration == nil {
		return reject
	}
	op := nc.Status.Configuration.Operation
	if err := ValidateGroupJournalIdentity(op); err != nil {
		return err
	}
	if op.PhysicalGroup.Digest != expected.Status.Configuration.Operation.PhysicalGroup.Digest || op.ControllerGeneration != nc.Generation || (op.Phase != "Prepared" && op.Phase != "Submitting") || op.TaskID != "" {
		return reject
	}
	if (op.Phase == "Prepared" && op.SubmittedAt != nil) || (op.Phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(observedNow()))) {
		return reject
	}
	prior, err := ValidateGroupReservationSet(&nc, reservations)
	if err != nil {
		return err
	}
	current := make([]*ops.ControllerConfigurationReservation, 0, len(prior))
	leases := make([]*coordination.Lease, 0, len(prior))
	observed := []client.Object{&nc}
	claimed := 0
	for _, old := range prior {
		var r ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(old), &r); err != nil {
			return err
		}
		if r.UID != old.UID || !reflect.DeepEqual(r.Spec, old.Spec) || !reflect.DeepEqual(r.Status.Claim, old.Status.Claim) || r.Spec.ControllerWorkerPod != pod || r.Status.Grant == nil {
			return reject
		}
		if c := r.Status.Claim; c != nil {
			claimed++
			if c.OperationID != op.ID || c.OperationDigest != r.Spec.OperationDigest || c.ClaimedAt.IsZero() || c.ClaimedAt.After(observedNow()) || (op.SubmittedAt != nil && op.SubmittedAt.Before(&c.ClaimedAt)) {
				return reject
			}
		}
		var lease coordination.Lease
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
			return err
		}
		current = append(current, &r)
		leases = append(leases, &lease)
		observed = append(observed, &r)
	}
	if (claimed != 0 && claimed != len(current)) || (op.Phase == "Submitting" && claimed != len(current)) {
		return reject
	}
	if err := ValidateGroupLiveGrants(&nc, current, leases, namespace, observedNow()); err != nil {
		return err
	}
	if err := readBoundConfigurationIntent(ctx, reader, &nc, current[0]); err != nil {
		return err
	}
	if err := ReadControllerWorkerBinding(ctx, reader, current[0], observedNow()); err != nil {
		return err
	}
	targets, err := CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return err
	}
	var all cvk.CiscoDeviceList
	if err := reader.List(ctx, &all); err != nil {
		return err
	}
	devices, err := exclusiveGroupDevices(nc.Namespace, targets, all.Items)
	if err != nil {
		return err
	}
	for i, r := range current {
		var node core.Node
		var worker core.Pod
		if err := reader.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &node); err != nil {
			return err
		}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.DeviceWorkerPod.Name}, &worker); err != nil {
			return err
		}
		if node.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] != worker.Name || node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] != string(worker.UID) {
			return reject
		}
		if err := validateDeviceWorkerObjects(r, devices[i], &node, &worker, observedNow()); err != nil {
			return err
		}
		if err := readGuardedGrantEvidence(ctx, reader, r, namespace, observedNow()); err != nil {
			return err
		}
		if err := readEmptyConfigurationWorkloads(ctx, reader, r); err != nil {
			return err
		}
		observed = append(observed, devices[i], &node, &worker)
	}
	var late cvk.CiscoDeviceList
	if err := reader.List(ctx, &late); err != nil {
		return err
	}
	latest, err := exclusiveGroupDevices(nc.Namespace, targets, late.Items)
	if err != nil {
		return err
	}
	for i := range latest {
		if latest[i].ResourceVersion != devices[i].ResourceVersion {
			return reject
		}
	}
	for _, old := range observed {
		fresh := old.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(old), fresh); err != nil {
			return err
		}
		if fresh.GetUID() != old.GetUID() || fresh.GetResourceVersion() != old.GetResourceVersion() {
			return reject
		}
	}
	// Source and connector observations precede the per-device scan. Repeat
	// them after it so a late replacement cannot hide behind a long group read.
	if err := readBoundConfigurationIntent(ctx, reader, &nc, current[0]); err != nil {
		return err
	}
	if err := ReadControllerWorkerBinding(ctx, reader, current[0], observedNow()); err != nil {
		return err
	}
	for _, r := range current {
		if err := readEmptyConfigurationWorkloads(ctx, reader, r); err != nil {
			return err
		}
	}
	return nil
}
