// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMetadataHandoffCopiesAndLegacyEncoding(t *testing.T) {
	at := metav1.Now()
	original := &NetworkControllerConfigurationStatus{MetadataHandoff: &NetworkControllerConfigurationMetadataHandoff{Protocol: NetworkControllerMetadataHandoffProtocol, BindingDigest: strings.Repeat("a", 64), LeaseNamespace: "leases", PhysicalIdentities: []string{"serial"}, Operation: NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32)}, RefusedSubmissionAt: &at, RefusedAt: at.DeepCopy()}}
	original.MetadataHandoff.ReadyAt = at.DeepCopy()
	original.MetadataHandoff.Fences = []NetworkControllerMetadataFenceReference{{PhysicalIdentity: "serial", Name: "original", UID: "uid"}}
	copy := original.DeepCopy()
	copy.MetadataHandoff.Fences[0].UID = "replacement"
	copy.MetadataHandoff.ReadyAt.Time = copy.MetadataHandoff.ReadyAt.AddDate(1, 0, 0)
	copy.MetadataHandoff.PhysicalIdentities[0] = "foreign"
	copy.MetadataHandoff.Operation.ID = strings.Repeat("c", 32)
	copy.MetadataHandoff.RefusedAt.Time = copy.MetadataHandoff.RefusedAt.AddDate(1, 0, 0)
	if !original.MetadataHandoff.ReadyAt.Equal(&at) || original.MetadataHandoff.Fences[0].UID != "uid" || original.MetadataHandoff.PhysicalIdentities[0] != "serial" || original.MetadataHandoff.Operation.ID != strings.Repeat("b", 32) || !original.MetadataHandoff.RefusedAt.Equal(&at) {
		t.Fatal("status copy aliases retained handoff evidence")
	}
	cancelled := &NetworkControllerConfigurationStatus{MetadataHandoff: &NetworkControllerConfigurationMetadataHandoff{CancelledAt: at.DeepCopy()}}
	cancelledCopy := cancelled.DeepCopy()
	cancelledCopy.MetadataHandoff.CancelledAt.Time = cancelledCopy.MetadataHandoff.CancelledAt.AddDate(1, 0, 0)
	if !cancelled.MetadataHandoff.CancelledAt.Equal(&at) {
		t.Fatal("cancellation timestamp aliases original custody")
	}
	raw, err := json.Marshal(&NetworkControllerConfigurationStatus{})
	if err != nil || strings.Contains(string(raw), "metadataHandoff") {
		t.Fatal("legacy status acquired an active handoff", err)
	}
}
