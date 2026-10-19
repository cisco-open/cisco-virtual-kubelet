// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const tagRetirementCondition = "TagOwnershipRetirement"
const tagRetirementProtocol = "tag-ownership-retirement:v1"

// A durable closure witness, not transfer or submission permission. The old
// owner claim and every original action receipt remain unchanged. A successor
// still needs old-CR absence, fresh native absence and a separate status CAS.
type tagOwnershipRetirement struct {
	Protocol, ModelVersion, ConfigName string
	Sections                           []string
	Completion                         featureConfigCompletion
	ArchiveDigest                      string `json:",omitempty"`
	RootSeal                           string `json:",omitempty"`
	RetainedDigest                     string
}

func tagRetirementRequired(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil || nc.Status.Configuration == nil {
		return false
	}
	own, _ := featureConfigEvidence(nc, c)
	return len(own.Owners) != 0 || len(own.ActionApplications) != 0 || len(own.CredentialApplications) != 0 || own.Operation != nil
}

func tagRetirementEvidence(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*cvk.NetworkControllerConfigurationStatus, error) {
	if nc == nil || c == nil || c.UID == "" || c.Name == "" || c.Generation < 1 || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete || !tagConfigKnownWorkClosed(nc, c) {
		return nil, errConfigurationOwned
	}
	if err := tagOwnerCustodyValid(nc, c); err != nil {
		return nil, err
	}
	own, _ := featureConfigEvidence(nc, c)
	if len(own.Owners) != len(c.Spec.ManagedSections) || len(own.ActionApplications) > 256 {
		return nil, errConfigurationOwned
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, c); err != nil {
		return nil, err
	}

	for _, row := range own.ActionApplications {
		if row.Operation.ConfigGeneration > c.Generation || row.Operation.ControllerGeneration > nc.Generation {
			return nil, errConfigurationOwned
		}
	}
	return own, nil
}

// The decoder is pure so the manager can independently reject a missing,
// malformed or changed retirement witness before removing a finalizer.
func tagRetirementRecord(nc *cvk.NetworkController) (*tagOwnershipRetirement, *cvk.NetworkControllerConfigurationStatus, error) {
	if nc == nil || nc.UID == "" || nc.Generation < 1 || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() {
		return nil, nil, errConfigurationOwned
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "TagConfigClosed" || cond.ObservedGeneration != nc.Generation || len(cond.Message) > 4096 {
		return nil, nil, errConfigurationOwned
	}
	var r tagOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &r) != nil {
		return nil, nil, errConfigurationOwned
	}
	canonical, _ := json.Marshal(r)
	p := r.Completion
	if string(canonical) != cond.Message || r.Protocol != tagRetirementProtocol || r.ModelVersion != nacmodel.Version || r.ConfigName == "" || p.ConfigUID == "" || p.ConfigGeneration < 1 || p.ControllerUID != string(nc.UID) || p.ControllerGeneration != nc.Generation || p.Policy != string(configapi.NetworkControllerRetentionPolicyDelete) {
		return nil, nil, errConfigurationOwned
	}
	for _, digest := range []string{p.SpecDigest, p.DeletionToken, p.EvidenceDigest, r.RetainedDigest} {
		if !validTemplateComparisonDigest(digest) {
			return nil, nil, errConfigurationOwned
		}
	}
	if !slices.IsSorted(r.Sections) {
		return nil, nil, errConfigurationOwned
	}
	c := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: r.ConfigName, UID: types.UID(p.ConfigUID), Generation: p.ConfigGeneration}, Spec: configapi.NetworkControllerConfigSpec{Mode: configapi.NetworkControllerApplyModeApply, Scope: "global", ManagedSections: r.Sections, DeletionPolicy: configapi.NetworkControllerRetentionPolicyDelete, ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Resolved: true, Format: nacmodel.Format, ModelVersion: nacmodel.Version}}}
	own, err := tagRetirementEvidence(nc, c)
	if err != nil || featureIdentityDigest(own) != r.RetainedDigest || p.EvidenceDigest != r.RetainedDigest {
		return nil, nil, errConfigurationOwned
	}
	return &r, own, nil
}

func tagRetirementAllowsCompletion(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil {
		return false
	}
	if c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete {
		cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
		if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason == "TagOwnershipHandedOff" {
			return true
		}
		r, _, err := tagRetirementRecord(nc)
		return err == nil && r.Completion.ConfigUID != string(c.UID)
	}
	if !tagRetirementRequired(nc, c) {
		return true
	}
	r, _, err := tagRetirementRecord(nc)
	sections := append([]string(nil), c.Spec.ManagedSections...)
	slices.Sort(sections)
	return err == nil && r.ConfigName == c.Name && reflect.DeepEqual(r.Sections, sections) && reflect.DeepEqual(r.Completion, featureConfigCompletionProof(nc, c))
}

// This checks deletion metadata without reading the original ConfigMap, which
// may already be gone after settled tag work. It grants no native write permit.
func (a *adapter) checkTagRetirementSnapshot(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*cvk.NetworkController, error) {
	if c.Namespace != a.key.Namespace || c.Spec.ControllerRef.Name != a.key.Name || tagDeletionContext(c, false) == nil {
		return nil, errConfigurationStale
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return nil, err
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || featureIdentityDigest(fresh.Spec) != featureIdentityDigest(c.Spec) || featureIdentityDigest(fresh.DeletionTimestamp) != featureIdentityDigest(c.DeletionTimestamp) || tagDeletionContext(&fresh, false) == nil {
		return nil, errConfigurationStale
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration != nil && current.Status.Configuration.Operation != nil {
		return nil, errConfigurationStale
	}
	return current, nil
}

func (a *adapter) retainTagOwnershipRetirement(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) error {
	old := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
	if c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete {
		if old == nil || old.Status != metav1.ConditionTrue || old.Reason == "TagOwnershipHandedOff" {
			return nil
		}
		var previous tagOwnershipRetirement
		if json.Unmarshal([]byte(old.Message), &previous) != nil {
			return errConfigurationOwned
		}
		if previous.Completion.ConfigUID != string(c.UID) {
			return nil
		}
		current, err := a.checkTagRetirementSnapshot(ctx, nc, c)
		if err != nil {
			return err
		}
		meta.SetStatusCondition(&current.Status.Conditions, condition(tagRetirementCondition, false, "TagConfigRetained", "Retain policy preserves tag ownership; no retirement authority is granted", current.Generation))
		if err := a.statusWriter.Status().Update(ctx, current); err != nil {
			return err
		}
		*nc = *current
		return nil
	}
	if !tagRetirementRequired(nc, c) {
		return nil
	}
	own, err := tagRetirementEvidence(nc, c)
	if err != nil {
		return err
	}
	if _, err := a.checkTagRetirementSnapshot(ctx, nc, c); err != nil {
		return err
	}
	absent, err := a.tagOwnedConfigAbsent(ctx, nc, c)
	if err != nil {
		return err
	}
	if !absent {
		return errConfigurationOwned
	}
	current, err := a.checkTagRetirementSnapshot(ctx, nc, c)
	if err != nil {
		return err
	}
	sections := append([]string(nil), c.Spec.ManagedSections...)
	slices.Sort(sections)
	record := tagOwnershipRetirement{Protocol: tagRetirementProtocol, ModelVersion: nacmodel.Version, ConfigName: c.Name, Sections: sections, Completion: featureConfigCompletionProof(nc, c), RetainedDigest: featureIdentityDigest(own)}
	graph, err := tagOwnerArchiveGraph(nc.Status.Configuration)
	if err != nil {
		return err
	}
	if graph.root != nil {
		record.ArchiveDigest, record.RootSeal = tagArchiveIdentity(graph)
	}
	raw, _ := json.Marshal(record)
	if old != nil && old.Status == metav1.ConditionTrue && old.Reason != "TagOwnershipHandedOff" {
		var previous tagOwnershipRetirement
		if json.Unmarshal([]byte(old.Message), &previous) != nil || previous.Completion.ConfigUID != string(c.UID) {
			return errConfigurationOwned
		}
		if old.Reason == "TagConfigClosed" && old.ObservedGeneration == nc.Generation && old.Message == string(raw) {
			return nil
		}
	}
	meta.SetStatusCondition(&current.Status.Conditions, condition(tagRetirementCondition, true, "TagConfigClosed", string(raw), current.Generation))
	if _, _, err := tagRetirementRecord(current); err != nil {
		return err
	}
	if err := a.statusWriter.Status().Update(ctx, current); err != nil {
		return err
	}
	*nc = *current
	return nil
}
