// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	coordination "k8s.io/api/coordination/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadMetadataFenceCohort corroborates every original Lease incarnation retained
// in an authoritative handoff. Missing, replaced, released or malformed evidence
// is an error, never permission to acquire or dispatch. The caller still needs
// live source, actor, installation and global consumer validation before writes.
// It neither reads native inventory nor changes or settles any fence.
func ReadMetadataFenceCohort(ctx context.Context, reader client.Reader, expected *cvk.NetworkController) ([]coordination.Lease, error) {
	reject := errors.New("original metadata fence cohort is unavailable or changed")
	if reader == nil || expected == nil || expected.UID == "" || expected.Status.Configuration == nil {
		return nil, reject
	}
	expected = expected.DeepCopy()
	h := expected.Status.Configuration.MetadataHandoff
	if h == nil || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || h.ReleasedAt != nil || len(h.Fences) == 0 || len(h.Fences) != len(h.PhysicalIdentities) || len(h.Fences) > 256 || h.Operation.Phase != "Prepared" || h.Operation.Action != "Delete" || h.Operation.TargetSerial != "" || h.Operation.SubmittedAt != nil {
		return nil, reject
	}
	checkController := func() error {
		var current cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &current); err != nil {
			return err
		}
		if current.UID != expected.UID || current.Generation != expected.Generation || current.DeletionTimestamp != nil || !reflect.DeepEqual(current.Spec, expected.Spec) || !reflect.DeepEqual(current.Status.Configuration, expected.Status.Configuration) {
			return reject
		}
		return nil
	}
	if err := checkController(); err != nil {
		return nil, err
	}
	serials := map[string]bool{}
	for _, serial := range h.PhysicalIdentities {
		canonical, err := topologyidentity.CanonicalPhysicalIdentity(serial)
		if err != nil || canonical != serial || serials[serial] {
			return nil, reject
		}
		serials[serial] = true
	}
	refs := append([]cvk.NetworkControllerMetadataFenceReference(nil), h.Fences...)
	slices.SortFunc(refs, func(a, b cvk.NetworkControllerMetadataFenceReference) int {
		if a.PhysicalIdentity < b.PhysicalIdentity {
			return -1
		}
		if a.PhysicalIdentity > b.PhysicalIdentity {
			return 1
		}
		return 0
	})
	leases := make([]coordination.Lease, 0, len(refs))
	uids := map[string]bool{}
	for _, ref := range refs {
		name, err := BootstrapFenceName(ref.PhysicalIdentity)
		if err != nil || name != ref.Name || !serials[ref.PhysicalIdentity] || ref.UID == "" || uids[ref.UID] {
			return nil, reject
		}
		delete(serials, ref.PhysicalIdentity)
		uids[ref.UID] = true
		var actual coordination.Lease
		if err := reader.Get(ctx, client.ObjectKey{Namespace: h.LeaseNamespace, Name: ref.Name}, &actual); err != nil {
			return nil, err
		}
		if !metadataFenceBindingMatches(expected, h, ref, &actual, "") {
			return nil, reject
		}
		leases = append(leases, *actual.DeepCopy())
	}
	if len(serials) != 0 {
		return nil, reject
	}
	// Re-read every incarnation after observing the cohort. This detects drift
	// during observation; it is not a cross-object transaction or a dispatch grant.
	for i := range leases {
		var current coordination.Lease
		if err := reader.Get(ctx, client.ObjectKeyFromObject(&leases[i]), &current); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(current, leases[i]) {
			return nil, reject
		}
	}
	if err := checkController(); err != nil {
		return nil, err
	}
	return leases, nil
}

// A released object must retain the exact original binding and UID. This is
// shape corroboration; terminal outcome and live authority are checked by settlement.
func metadataFenceBindingMatches(nc *cvk.NetworkController, h *cvk.NetworkControllerConfigurationMetadataHandoff, ref cvk.NetworkControllerMetadataFenceReference, object *coordination.Lease, releaseDigest string) bool {
	if nc == nil || h == nil || object == nil {
		return false
	}
	actual := *object
	if string(actual.UID) != ref.UID || actual.Labels[metadataFenceLabel] != MetadataFenceProtocol {
		return false
	}
	var record bootstrapFenceRecord
	if json.Unmarshal([]byte(actual.Annotations[bootstrapFenceBindingAnnotation]), &record) != nil || record.Protocol != BootstrapFenceProtocol || record.OwnerNamespace != nc.Namespace {
		return false
	}
	i := record.Identity
	op := h.Operation
	if i.PhysicalIdentity != ref.PhysicalIdentity || i.OperationID != op.ID || i.OperationDigest != h.BindingDigest || i.Controller.Name != nc.Name || i.Controller.UID != string(nc.UID) || i.ControllerGeneration != op.ControllerGeneration || op.ControllerGeneration < 1 || op.ControllerGeneration > nc.Generation || i.Config.Name != op.ConfigName || i.Config.UID != op.ConfigUID || i.ConfigGeneration != op.ConfigGeneration || i.NativeTargetID != op.TargetID || i.SourceUID != op.SourceUID || i.SourceVersion != op.SourceVersion || record.Worker.Pod != h.WorkerPod || record.Worker.Username != h.WorkerUsername {
		return false
	}
	candidate, err := bootstrapFenceCandidate(h.LeaseNamespace, nc.Namespace, &i, &record.Worker, record.Installation, &record.Protection)
	if err != nil {
		return false
	}
	candidate.Labels[metadataFenceLabel] = MetadataFenceProtocol
	if releaseDigest != "" {
		candidate.Annotations[metadataFenceReleaseAnnotation] = releaseDigest
		candidate.Finalizers = nil
	}
	// Compare the complete retained shape, including no release acknowledgement.
	if actual.ResourceVersion == "" || actual.DeletionTimestamp != nil || len(actual.OwnerReferences) != 0 || !reflect.DeepEqual(actual.Labels, candidate.Labels) || !reflect.DeepEqual(actual.Annotations, candidate.Annotations) || !slices.Equal(actual.Finalizers, candidate.Finalizers) || !reflect.DeepEqual(actual.Spec, candidate.Spec) {
		return false
	}

	return true
}
