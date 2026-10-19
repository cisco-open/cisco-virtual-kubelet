// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Called only after the existing original pre-HTTP capability was consumed and
// exact journal/owner checks passed. This helper cannot mint or infer that proof.
// A handoff is staged custody, never permission to enter native submission.
func (a *adapter) retainPnPMetadataRefusal(state *cvk.NetworkControllerConfigurationStatus, before *cvk.NetworkControllerConfigurationOperation) error {
	h := state.MetadataHandoff
	if h == nil {
		return nil
	} // Preserve unfenced legacy execution.
	if !validPnPRemovalOperation(before) || before.Phase != "Submitting" || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || !validTemplateComparisonDigest(h.BindingDigest) || len(validation.IsDNS1123Label(h.LeaseNamespace)) != 0 || a.workerPodName == "" || a.workerPodUID == "" || h.WorkerPod.Name != a.workerPodName || h.WorkerPod.UID != a.workerPodUID || h.CancelledAt != nil || h.RefusedAt != nil || h.RefusedSubmissionAt != nil || h.ReleasedAt != nil || len(h.PhysicalIdentities) == 0 || len(h.PhysicalIdentities) > 256 {
		return errConfigurationOwned
	}
	prepared := before.DeepCopy()
	prepared.Phase = "Prepared"
	prepared.SubmittedAt = nil
	if !reflect.DeepEqual(*prepared, h.Operation) {
		return errConfigurationStale
	}
	serials := map[string]bool{}
	for _, serial := range h.PhysicalIdentities {
		canonical, err := topologyidentity.CanonicalPhysicalIdentity(serial)
		if err != nil || serial != canonical || serials[serial] {
			return errConfigurationOwned
		}
		serials[serial] = true
	}
	now := metav1.NewTime(time.Now().UTC())
	if before.SubmittedAt.After(now.Time) {
		return errConfigurationStale
	}
	h.RefusedSubmissionAt = before.SubmittedAt.DeepCopy()
	h.RefusedAt = &now
	return nil
}
