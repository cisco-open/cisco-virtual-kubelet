// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/types"
)

func preparedPnPRemovalScopeSelected(nc *cvk.NetworkController) bool {
	return nc != nil && nc.Spec.Type == TypeName && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && pnpRemovalSelected(nc.Status.Configuration.Operation.Resource)
}

// Pure manager projection: never reconstruct omitted serials from current model
// names, adopt a legacy receipt, choose a later UUID or manufacture TargetSerial.
// Native cohort preflight and installed actor admission are separate gates.
func preparedPnPRemovalPhysicalIdentities(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source controlleradapter.ConfigurationMetadataSource) ([]string, error) {
	if !preparedPnPRemovalScopeSelected(nc) || c == nil {
		return nil, errConfigurationTransition
	}
	nc = nc.DeepCopy()
	c = c.DeepCopy()
	source.Raw = append([]byte(nil), source.Raw...)
	op := nc.Status.Configuration.Operation
	if !validPnPRemovalOperation(op) || op.Phase != "Prepared" || op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || op.ConfigGeneration != c.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != source.UID || op.SourceVersion != source.Version || c.Namespace != nc.Namespace || c.Spec.ControllerRef.Name != nc.Name || nc.Spec.Paused || nc.DeletionTimestamp != nil {
		return nil, errConfigurationOwned
	}
	if (c.Spec.Source.Inline == nil) == (c.Spec.Source.ConfigMapRef == nil) {
		return nil, errConfigurationStale
	}
	if c.Spec.Source.Inline != nil {
		if source.UID != "" || source.Version != "" || !bytes.Equal(source.Raw, c.Spec.Source.Inline.Raw) {
			return nil, errConfigurationStale
		}
	} else if source.UID == "" || source.Version == "" || c.Spec.Source.ConfigMapRef.Name == "" || c.Spec.Source.ConfigMapRef.Key == "" {
		return nil, errConfigurationStale
	}
	s := &configSourceSnapshot{raw: source.Raw, uid: types.UID(source.UID), version: source.Version}
	cohort, err := ownedPnPRemovalCohort(nc, c, s)
	if err != nil {
		return nil, err
	}
	if len(cohort) == 0 || cohort[0].Baseline.ID != op.TargetID || op.Resource != pnpRemovalJournalPrefix+op.TargetID+":"+featureIdentityDigest(cohort[0].Creator) {
		return nil, errConfigurationOwned
	}
	if err := pnpRemovalAdmission(nc.Status.Configuration, c, s, op); err != nil {
		return nil, err
	}
	serials := make([]string, 0, len(cohort))
	for _, entry := range cohort {
		serials = append(serials, entry.Baseline.Serial)
	}
	sort.Strings(serials)
	// Do not return a mutable alias of caller-owned source/status data.
	return serials, nil
}
