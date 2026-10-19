// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MetadataScopeWorkerObservation binds read-only scope facts to one current
// controller worker incarnation. It is not an authorization, retained fence or
// submission permit. Do not reconstruct a physical reservation from this value.
type MetadataScopeWorkerObservation struct {
	Controller, Config          ops.ConfigurationReservationReference
	OperationID, BindingDigest  string
	Deployment, ReplicaSet, Pod ops.ConfigurationReservationReference
	Username, ContractHash      string
	PhysicalIdentities          []string
}

// ReadMetadataScopeWorker reuses the bootstrap/controller-worker validators and
// source bindings without creating a CiscoDevice or physical TargetSerial. The
// caller supplies the independently validated complete creator serial cohort.
// Cross-object reads cannot serialize future enrollment or authorize dispatch.
func ReadMetadataScopeWorker(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, leaseNamespace string, serials []string, now time.Time) (*MetadataScopeWorkerObservation, error) {
	return readMetadataScopeWorker(ctx, reader, expected, leaseNamespace, serials, now, nil)
}

func readMetadataScopeWorker(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, leaseNamespace string, serials []string, now time.Time, fenceCheck func(string) error) (*MetadataScopeWorkerObservation, error) {
	reject := errors.New("metadata scope has no stable current controller-worker incarnation")
	if expected == nil || now.IsZero() {
		return nil, reject
	}
	expected = expected.DeepCopy()
	serials = append([]string(nil), serials...)
	slices.Sort(serials)
	if err := checkUnmanagedMetadataScope(ctx, reader, expected, leaseNamespace, serials, fenceCheck); err != nil {
		return nil, err
	}
	first, err := readBootstrapControllerWorker(ctx, reader, expected, now)
	if err != nil {
		return nil, err
	}
	op := expected.Status.Configuration.Operation
	ref := ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}
	if err := readBoundConfigurationSource(ctx, reader, expected, expected.Namespace, ref, op.ConfigGeneration); err != nil {
		return nil, err
	}
	second, err := readBootstrapControllerWorker(ctx, reader, expected, now)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(first, second) {
		return nil, reject
	}
	if err := readBoundConfigurationSource(ctx, reader, expected, expected.Namespace, ref, op.ConfigGeneration); err != nil {
		return nil, err
	}
	return bindMetadataScopeWorker(expected, first, serials)
}

// Pure binding shared by manager observation and worker claim. Callers perform
// their own role-appropriate source, actor and scope checks first.
func bindMetadataScopeWorker(expected *cvk.NetworkController, first *bootstrapControllerWorker, serials []string) (*MetadataScopeWorkerObservation, error) {
	op := expected.Status.Configuration.Operation
	ref := ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}
	// Bind full existing non-secret creator/owner/journal evidence as well as the
	// canonical cohort. Never hash raw model content or credential values here.
	snapshot := expected.Status.Configuration.DeepCopy()
	snapshot.MetadataHandoff = nil // The sealed binding must not hash itself.
	raw, err := json.Marshal(struct {
		Namespace     string
		Controller    ops.ConfigurationReservationReference
		Generation    int64
		Configuration *cvk.NetworkControllerConfigurationStatus
		Serials       []string
		Worker        *bootstrapControllerWorker
	}{expected.Namespace, ops.ConfigurationReservationReference{Name: expected.Name, UID: string(expected.UID)}, expected.Generation, snapshot, serials, first})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &MetadataScopeWorkerObservation{Controller: ops.ConfigurationReservationReference{Name: expected.Name, UID: string(expected.UID)}, Config: ref, OperationID: op.ID, BindingDigest: hex.EncodeToString(sum[:]), Deployment: first.Deployment, ReplicaSet: first.ReplicaSet, Pod: first.Pod, Username: first.Username, ContractHash: first.ContractHash, PhysicalIdentities: serials}, nil
}
