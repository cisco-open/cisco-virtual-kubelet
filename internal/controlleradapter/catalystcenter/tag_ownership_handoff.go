// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"slices"
	"strings"
)

const tagHandoffProtocol = "tag-ownership-handoff:v1"

type tagOwnershipHandoff struct {
	Protocol, ControllerUID                                                    string
	ControllerGeneration                                                       int64
	Previous                                                                   tagOwnershipRetirement
	ConfigName, ConfigUID                                                      string
	ConfigGeneration                                                           int64
	Sections                                                                   []string
	SourceUID, SourceVersion, SpecDigest, InputDigest, ArchiveDigest, RootSeal string
}

func tagArchiveIdentity(g tagOwnerGraph) (string, string) {
	seal := ""
	if g.root != nil {
		seal = g.root.Seal
	}
	return featureCohortDigest(g.rows), seal
}
func tagOwnerCustodyIdentity(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil {
		return false
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "TagOwnershipHandedOff" {
		return false
	}
	var h tagOwnershipHandoff
	return json.Unmarshal([]byte(cond.Message), &h) == nil && h.ConfigUID == string(c.UID) && h.ConfigName == c.Name
}
func tagOwnerCustodyOwner(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil {
		return false
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "TagOwnershipHandedOff" {
		return false
	}
	var h tagOwnershipHandoff
	if json.Unmarshal([]byte(cond.Message), &h) != nil || h.ConfigUID != string(c.UID) || h.ConfigName != c.Name {
		return false
	}
	return tagOwnerCustodyValid(nc, c) == nil
}

// Historical custody is not a native submission permit. Current source,
// generation, worker and journal checks still govern every new operation.
func tagOwnerCustodyValid(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) error {
	if nc == nil || c == nil {
		return errConfigurationOwned
	}
	g, err := tagOwnerArchiveGraph(nc.Status.Configuration)
	if err != nil {
		return err
	}
	digest, seal := tagArchiveIdentity(g)
	cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
	if cond == nil {
		if g.root != nil {
			return errConfigurationOwned
		}
		return nil
	}
	if cond.Status != metav1.ConditionTrue {
		if g.root != nil {
			return errConfigurationOwned
		}
		return nil
	}
	switch cond.Reason {
	case "TagOwnershipHandedOff":
		var h tagOwnershipHandoff
		if json.Unmarshal([]byte(cond.Message), &h) != nil {
			return errConfigurationOwned
		}
		raw, _ := json.Marshal(h)
		sections := append([]string(nil), c.Spec.ManagedSections...)
		slices.Sort(sections)
		if string(raw) != cond.Message || h.Protocol != tagHandoffProtocol || h.ControllerUID != string(nc.UID) || h.ControllerGeneration < 1 || h.ControllerGeneration > nc.Generation || cond.ObservedGeneration != h.ControllerGeneration || h.ConfigUID != string(c.UID) || h.ConfigName != c.Name || h.ConfigGeneration < 1 || h.ConfigGeneration > c.Generation || !reflect.DeepEqual(h.Sections, sections) || h.ArchiveDigest != digest || h.RootSeal != seal || h.Previous.Completion.ConfigUID == h.ConfigUID || h.Previous.Completion.ControllerUID != string(nc.UID) || h.Previous.Completion.Policy != "Delete" {
			return errConfigurationOwned
		}
		for _, v := range []string{h.SpecDigest, h.InputDigest, h.ArchiveDigest} {
			if !validTemplateComparisonDigest(v) {
				return errConfigurationOwned
			}
		}
		if (h.SourceUID == "") != (h.SourceVersion == "") {
			return errConfigurationOwned
		}
		if nc.Status.Configuration == nil || configurationOwnersMatch(nc.Status.Configuration, c) != nil {
			return errConfigurationOwned
		}
	case "TagConfigClosed":
		var r tagOwnershipRetirement
		if json.Unmarshal([]byte(cond.Message), &r) != nil {
			return errConfigurationOwned
		}
		raw, _ := json.Marshal(r)
		if string(raw) != cond.Message || r.Protocol != tagRetirementProtocol || r.ModelVersion != nacmodel.Version || r.Completion.ConfigUID != string(c.UID) || r.ConfigName != c.Name || r.Completion.ControllerUID != string(nc.UID) || r.Completion.ControllerGeneration > nc.Generation {
			return errConfigurationOwned
		}
		if g.root == nil {
			if r.RootSeal != "" || r.ArchiveDigest != "" && r.ArchiveDigest != digest {
				return errConfigurationOwned
			}
		} else if r.RootSeal != seal || r.ArchiveDigest != digest {
			return errConfigurationOwned
		}
	default:
		if g.root != nil {
			return errConfigurationOwned
		}
	}
	return nil
}
func (a *adapter) tagPreviousConfigGone(ctx context.Context, r *tagOwnershipRetirement) error {
	var previous configapi.NetworkControllerConfig
	err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: a.key.Namespace, Name: r.ConfigName}, &previous)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(previous.UID) == r.Completion.ConfigUID {
		return errConfigurationOwned
	}
	return nil
}
func tagRetiredOwnerConfig(r *tagOwnershipRetirement) *configapi.NetworkControllerConfig {
	return &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: r.ConfigName, UID: types.UID(r.Completion.ConfigUID), Generation: r.Completion.ConfigGeneration}, Spec: configapi.NetworkControllerConfigSpec{Mode: configapi.NetworkControllerApplyModeApply, Scope: "global", ManagedSections: r.Sections, DeletionPolicy: configapi.NetworkControllerRetentionPolicyDelete, ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Resolved: true, Format: nacmodel.Format, ModelVersion: nacmodel.Version}}}
}
func (a *adapter) handoffTagOwnership(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, error) {
	if c == nil || nc == nil || source == nil || !c.DeletionTimestamp.IsZero() || !tagDeletionShape(c) || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete || !hasTagCleanupFinalizer(c) || !tagPruneSource(source.raw, c.Spec.ManagedSections) {
		return false, nil
	}
	if !supportedConfigurationApply(c) || controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath) != nil {
		return false, nil
	}
	if nc.Status.Configuration == nil {
		return false, nil
	}
	foreign := false
	for _, o := range nc.Status.Configuration.Owners {
		for _, section := range c.Spec.ManagedSections {
			if configurationSectionsOverlap(o.Section, section) && (o.ConfigUID != string(c.UID) || o.ConfigName != c.Name) {
				foreign = true
			}
		}
	}
	if !foreign {
		if err := tagOwnerCustodyValid(nc, c); err != nil {
			return true, err
		}
		return false, nil
	}
	// Empty inventory without a tag key remains discovery's explicit protocol.
	if pureDiscoverySource(source.raw) {
		return false, nil
	}
	if nc.Status.Configuration.Operation != nil {
		return true, errConfigurationBusy
	}
	retired, _, err := tagRetirementRecord(nc)
	if err != nil {
		return false, nil
	} // Other resource families keep their existing handoff.
	sections := append([]string(nil), c.Spec.ManagedSections...)
	slices.Sort(sections)
	if tagOwnerScope(sections) == "" || retired.Completion.ConfigUID == string(c.UID) {
		return true, errConfigurationOwned
	}
	if _, err := a.client.compileConfigurationApply(source.raw, c.Spec.ManagedSections, source.templateFiles); err != nil {
		return true, err
	}
	old := tagRetiredOwnerConfig(retired)
	var baseline map[string]observedTag
	for i := range 2 {
		if err := a.tagPreviousConfigGone(ctx, retired); err != nil {
			return true, err
		}
		absent, err := a.tagOwnedConfigAbsent(ctx, nc, old)
		if err != nil {
			return true, err
		}
		if !absent {
			return true, errConfigurationOwned
		}
		actual, err := a.client.listTags(ctx)
		if err != nil {
			return true, err
		}
		if i == 0 {
			baseline = actual
		} else if !reflect.DeepEqual(baseline, actual) {
			return true, errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return true, err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return true, err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || current.Spec.Paused || !current.DeletionTimestamp.IsZero() {
			return true, errConfigurationStale
		}
		if err := a.tagPreviousConfigGone(ctx, retired); err != nil {
			return true, err
		}
	}
	if _, err := archiveTagOwner(nc.Status.Configuration, retired); err != nil {
		return true, err
	}
	// Cleanup retired the old owner's entire shared tag namespace. Replace its
	// section claims atomically; a different source representation must neither
	// inherit omitted claims nor split custody with another inventory/templates
	// owner. The sealed audit retains each historical owner's original scope.
	owners, err := tagHandoffOwners(nc.Status.Configuration.Owners, retired, c)
	if err != nil {
		return true, err
	}
	nc.Status.Configuration.Owners = owners
	if err := configurationOwnersMatch(nc.Status.Configuration, c); err != nil {
		return true, err
	}
	g, err := tagOwnerArchiveGraph(nc.Status.Configuration)
	if err != nil {
		return true, err
	}
	digest, seal := tagArchiveIdentity(g)
	h := tagOwnershipHandoff{Protocol: tagHandoffProtocol, ControllerUID: string(nc.UID), ControllerGeneration: nc.Generation, Previous: *retired, ConfigName: c.Name, ConfigUID: string(c.UID), ConfigGeneration: c.Generation, Sections: sections, SourceUID: string(source.uid), SourceVersion: source.version, SpecDigest: featureIdentityDigest(c.Spec), InputDigest: featureIdentityDigest(source.raw), ArchiveDigest: digest, RootSeal: seal}
	raw, _ := json.Marshal(h)
	if len(raw) > 4096 {
		return true, errConfigurationBusy
	}
	meta.SetStatusCondition(&nc.Status.Conditions, condition(tagRetirementCondition, true, "TagOwnershipHandedOff", string(raw), nc.Generation))
	if tagOwnerCustodyValid(nc, c) != nil {
		return true, errConfigurationOwned
	}
	body, _ := json.Marshal(nc.Status.Configuration)
	if len(body) > 250<<10 || len(nc.Status.Configuration.ActionApplications) > 256 {
		return true, errConfigurationBusy
	}
	return true, a.statusWriter.Status().Update(ctx, nc)
}

// Only a proven, fully retired owner can exchange section claims. Unrelated
// section owners survive byte-for-byte; overlapping foreign or partial claims
// are never displaced by a handoff.
func tagHandoffOwners(owners []cvk.NetworkControllerSectionOwner, retired *tagOwnershipRetirement, c *configapi.NetworkControllerConfig) ([]cvk.NetworkControllerSectionOwner, error) {
	if retired == nil || c == nil || tagOwnerScope(retired.Sections) == "" || tagOwnerScope(c.Spec.ManagedSections) == "" || retired.Completion.ConfigUID == string(c.UID) {
		return nil, errConfigurationOwned
	}
	kept := make([]cvk.NetworkControllerSectionOwner, 0, len(owners)+len(c.Spec.ManagedSections))
	seen := map[string]bool{}
	for _, o := range owners {
		if o.ConfigUID == retired.Completion.ConfigUID && o.ConfigName == retired.ConfigName {
			if !slices.Contains(retired.Sections, o.Section) || seen[o.Section] {
				return nil, errConfigurationOwned
			}
			seen[o.Section] = true
			continue
		}
		for _, section := range c.Spec.ManagedSections {
			if configurationSectionsOverlap(o.Section, section) {
				return nil, errConfigurationOwned
			}
		}
		kept = append(kept, o)
	}
	if len(seen) != len(retired.Sections) || len(kept)+len(c.Spec.ManagedSections) > 64 {
		return nil, errConfigurationOwned
	}
	for _, section := range c.Spec.ManagedSections {
		kept = append(kept, cvk.NetworkControllerSectionOwner{Section: section, ConfigUID: string(c.UID), ConfigName: c.Name})
	}
	slices.SortFunc(kept, func(a, b cvk.NetworkControllerSectionOwner) int { return strings.Compare(a.Section, b.Section) })
	return kept, nil
}
