// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Observation only. This is neither an ownership-transfer nor a native dispatch
// capability. The public bridge separately publishes custody and leaves native
// planning and dispatch to a later reconcile.
type discoveryCredentialHandoffObservation struct {
	ControllerVersion, ConfigUID, ConfigVersion, SpecDigest             string
	SourceUID, SourceVersion, ModelDigest, EvidenceDigest, InputsDigest string
	RetiringConfigUID, RetirementDigest                                 string
}

// Validate the existing typed closure while the original inventory owner is
// still reserved. Do not interpret a no-submission witness as delivered history
// or ask the ordinary handoff parser to accept credential records.
func discoveryCredentialRetiredHandoffRecord(nc *cvk.NetworkController) (*discoveryOwnershipRetirement, *discoveryCredentialLineage, error) {
	if nc == nil || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return nil, nil, errConfigurationOwned
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DiscoveryConfigClosed" || cond.ObservedGeneration != nc.Generation {
		return nil, nil, errConfigurationOwned
	}
	var record discoveryOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &record) != nil {
		return nil, nil, errConfigurationOwned
	}
	if record.UnsubmittedOperation != nil || strings.HasPrefix(record.Completion.CredentialDigest, discoveryCredentialUnsubmittedPrefix) {
		return discoveryCredentialSealedUnsubmittedRecord(nc)
	}
	raw, _ := json.Marshal(record)
	p := record.Completion
	if string(raw) != cond.Message || record.UnsubmittedOperation != nil || record.CredentialCustody != nil || record.ConfigName == "" || p.ConfigUID == "" || p.ConfigGeneration < 1 || p.ControllerUID != string(nc.UID) || p.ControllerGeneration != nc.Generation || p.SourceUID != "" || p.SourceVersion != "" || p.ModelDigest != "" || p.ScopeDigest != "" || !strings.HasPrefix(p.CredentialDigest, discoveryCredentialCompletionPrefix) {
		return nil, nil, errConfigurationOwned
	}
	for _, digest := range []string{p.SpecDigest, p.DeletionDigest, p.EvidenceDigest, record.RetainedDigest, record.RetiredDigest} {
		if !lowerDiscoveryDigest(digest) {
			return nil, nil, errConfigurationOwned
		}
	}
	previous := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: record.ConfigName, UID: types.UID(p.ConfigUID), Generation: p.ConfigGeneration}, Spec: configapi.NetworkControllerConfigSpec{ManagedSections: []string{"inventory"}}}
	selected, err := discoveryCredentialClosedEvidence(nc, previous)
	if err != nil {
		return nil, nil, err
	}
	if featureIdentityDigest(selected) != record.RetainedDigest || p.CredentialDigest != discoveryCredentialCompletionPrefix+record.RetainedDigest || p.EvidenceDigest != featureIdentityDigest(nc.Status.Configuration) {
		return nil, nil, errConfigurationOwned
	}
	retired, err := discoveryRetiredRows(nc, "")
	if err != nil || featureIdentityDigest(retired) != record.RetiredDigest {
		return nil, nil, errConfigurationOwned
	}
	cohort, err := discoveryCredentialHistoryCohort(nc.Status.Configuration, p.ConfigUID)
	if err != nil {
		return nil, nil, err
	}
	// A later closure must carry the complete newly verified cohort seal.
	// Legacy unsealed records remain readable only for a single owner.
	if len(cohort.archived) != 0 && record.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+cohort.allDigest {
		return nil, nil, errConfigurationOwned
	}
	// Legacy single-owner closures remain readable, but only a newly verified
	// seal may authorize historical execution in a future custody protocol.
	if record.CredentialHistoryDigest != "" && record.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+cohort.allDigest {
		return nil, nil, errConfigurationOwned
	}
	return &record, cohort.executionLineage(), nil
}

// A different config UID starts a new write-only delivery contract. Reused job
// fields must advance every delivered high-water mark, including removed leaves.
// Queued versions on a Delete/Prepared operation never count as delivery.
func discoveryCredentialSuccessorVersions(history *discoveryCredentialLineage, name string, versions []cvk.NetworkControllerConfigurationCredentialVersion) error {
	if history == nil {
		return errConfigurationOwned
	}
	for _, archive := range history.archives {
		if err := discoveryCredentialSuccessorVersions(archive, name, versions); err != nil {
			return err
		}
	}
	floors := map[string]int64{}
	for _, creator := range history.creators {
		if creator.identity.name != name {
			continue
		}
		for _, version := range creator.application.Versions {
			field := discoveryCredentialFieldForPath(version.Path)
			if field == "" {
				return errConfigurationSecret
			}
			if version.Version > floors[field] {
				floors[field] = version.Version
			}
		}
	}
	for _, version := range versions {
		field := discoveryCredentialFieldForPath(version.Path)
		if field == "" || version.Version < 1 || floors[field] > 0 && version.Version <= floors[field] {
			return errConfigurationSecret
		}
	}
	return nil
}

// A read-only prerequisite for typed handoff. Reuse the existing source,
// descriptor/projection, lineage and old-config absence checks. No status field,
// owner, receipt, source or finalizer is changed, even when a candidate is valid.
func (a *adapter) observeDiscoveryCredentialOwnershipHandoff(ctx context.Context, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (*discoveryCredentialHandoffObservation, error) {
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if !discoveryDeletionEligible(nc, c) || !hasDiscoveryConfigFinalizer(c) || !discoveryCredentialApplyShape(c) || source == nil {
		return nil, errConfigurationTransition
	}
	record, history, err := discoveryCredentialRetiredHandoffRecord(nc)
	if err != nil {
		return nil, err
	}
	if record.Completion.ConfigUID == string(c.UID) {
		return nil, errConfigurationOwned
	}
	if err := a.discoveryRetiredConfigGone(ctx, record); err != nil {
		return nil, err
	}
	plan, err := a.compileDiscoveryCredentialApply(nc, c, source)
	if err != nil {
		return nil, err
	}
	executor, ok := plan.executor.(*discoveryCredentialExecutor)
	if !ok || executor.count() == 0 || executor.ordinary != nil {
		return nil, errConfigurationOwned
	}
	var native map[string]discoveryMetadata
	var inputs []string
	for pass := 0; pass < 2; pass++ {
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return nil, err
		}
		var globals map[string]map[string]globalCredentialMetadata
		for _, want := range executor.projection.intents {
			if len(want.base.credentials) > 0 {
				globals, err = a.client.globalCredentials(ctx)
				if err != nil {
					return nil, err
				}
				break
			}
		}
		currentInputs := []string{}
		for index, want := range executor.projection.intents {
			request, input, err := executor.projection.bindRequest(nc, c, source, index, globals)
			if err != nil {
				return nil, err
			}
			if err := discoveryCredentialSuccessorVersions(history, discoveryPublicDigest(want.base.name), request.versions); err != nil {
				return nil, err
			}
			currentInputs = append(currentInputs, input)
		}
		if pass == 0 {
			inputs = currentInputs
		} else if !slices.Equal(inputs, currentInputs) {
			return nil, errConfigurationStale
		}

		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return nil, err
		}
		if err := history.absentRetired(actual); err != nil {
			return nil, err
		}
		if err := discoveryCredentialUnsubmittedAbsent(record, actual); err != nil {
			return nil, err
		}
		for _, want := range executor.projection.intents {
			if _, exists := actual[want.base.name]; exists {
				return nil, errConfigurationOwned
			}
		}
		if pass == 0 {
			native = actual
		} else if !reflect.DeepEqual(native, actual) {
			return nil, errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return nil, err
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return nil, err
		}
		if fresh.ResourceVersion != nc.ResourceVersion || fresh.UID != nc.UID || fresh.Generation != nc.Generation || !reflect.DeepEqual(fresh.Status, nc.Status) {
			return nil, errConfigurationStale
		}
		if err := a.discoveryRetiredConfigGone(ctx, record); err != nil {
			return nil, err
		}
		// Validate projected bytes again after the native read. The observation
		// cannot outlive source/projection drift within either pass.
		for index := range executor.projection.intents {
			if _, err := executor.projection.requestFields(fresh, c, source, index); err != nil {
				return nil, err
			}
		}
	}
	return &discoveryCredentialHandoffObservation{ControllerVersion: nc.ResourceVersion, ConfigUID: string(c.UID), ConfigVersion: c.ResourceVersion, SpecDigest: featureIdentityDigest(c.Spec), SourceUID: string(source.uid), SourceVersion: source.version, ModelDigest: featureIdentityDigest(source.raw), EvidenceDigest: featureIdentityDigest(nc.Status.Configuration), InputsDigest: featureIdentityDigest(inputs), RetiringConfigUID: record.Completion.ConfigUID, RetirementDigest: featureIdentityDigest(record)}, nil
}
