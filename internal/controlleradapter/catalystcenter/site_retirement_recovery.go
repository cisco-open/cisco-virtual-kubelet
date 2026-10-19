// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Preserve ancestor identity after the leaf is absent. Full native attributes
// stay fenced before dispatch; parent settings are not an effect of leaf Delete.
// This domain-separated proof can be recomputed without retaining a raw path or
// the deleted object's private native attributes in status.
func siteRetirementAncestry(actual []Site, parentID, kind string) ([]byte, error) {
	byPath := map[string]Site{}
	ids := map[string]bool{}
	var parent Site
	for _, s := range actual {
		if !validAPIID(s.ID) || ids[s.ID] || !validSitePath(s.NameHierarchy) {
			return nil, errInvalidResponse
		}
		if _, exists := byPath[s.NameHierarchy]; exists {
			return nil, errInvalidResponse
		}
		ids[s.ID] = true
		byPath[s.NameHierarchy] = s
		if s.ID == parentID {
			parent = s
		}
	}
	if parent.ID == "" || !validSiteParent(kind, parent.Type) {
		return nil, errConfigurationOwned
	}
	chain := []Site{}
	for at := parent.NameHierarchy; ; at = parentSitePath(at) {
		s, ok := byPath[at]
		if !ok || s.Name != lastSiteName(at) {
			return nil, errInvalidResponse
		}
		if len(chain) > 0 {
			child := chain[len(chain)-1]
			if child.ParentID != s.ID || !validSiteParent(child.Type, s.Type) {
				return nil, errInvalidResponse
			}
		}
		chain = append(chain, Site{ID: s.ID, Name: s.Name, NameHierarchy: s.NameHierarchy, ParentID: s.ParentID, Type: s.Type})
		if at == "Global" {
			if s.Type != "global" {
				return nil, errInvalidResponse
			}
			break
		}
	}
	return json.Marshal(chain)
}

func siteRetirementKnownTask(op *cvk.NetworkControllerConfigurationOperation) bool {
	return siteRetirementOperation(op) && (op.Phase == "Accepted" || op.Phase == "Unknown" || op.Phase == "Verifying") && configurationOperationID.MatchString(op.ID) && op.ConfigName != "" && op.ConfigUID != "" && op.ConfigGeneration > 0 && op.ControllerGeneration > 0 && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero()
}

func siteRetirementLineage(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) error {
	if _, err := siteCreatedRecords(state); err != nil {
		return err
	}
	if err := siteHistoryTaskUnused(state, op); err != nil {
		return err
	}
	return siteRetirementLineageCurrent(state, config, op)
}

func siteRetirementLineageCurrent(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) error {
	if !siteRetirementOperation(op) || state == nil || config == nil || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation {
		return errConfigurationOwned
	}
	if err := configurationOwnersMatch(state, config); err != nil {
		return err
	}
	parts := strings.Split(strings.TrimPrefix(op.Resource, siteRetirementPrefix), ":")
	creators, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return err
	}
	applied, err := siteAppliedRecordsCurrent(state)
	if err != nil {
		return err
	}
	var creator siteCreatorRecord
	for _, row := range creators {
		if siteReceiptDigest(row.receipt) == parts[4] {
			creator = row
		}
	}
	if creator.id != op.TargetID || creator.kind != parts[0] || creator.keyID != parts[3] || creator.receipt.Operation.ParentID != op.ParentID || siteResourcePathDigest(creator.receipt.Operation.Resource) != parts[1] || creator.receipt.Operation.ConfigName != op.ConfigName || creator.receipt.Operation.ConfigUID != op.ConfigUID {
		return errConfigurationOwned
	}
	latest, found := applied[parts[1]]
	previous, verified := &creator.receipt.Operation, creator.receipt.VerifiedAt
	if found {
		if siteReceiptDigest(latest) != parts[5] || len(latest.EffectReceipts) != 0 {
			return errConfigurationOwned
		}
		previous, verified = &latest.Operation, latest.VerifiedAt
	} else if parts[5] != strings.Repeat("0", 64) {
		return errConfigurationOwned
	}
	if op.ConfigGeneration < previous.ConfigGeneration || op.ControllerGeneration < previous.ControllerGeneration || !configurationInputAfter(op, previous) || op.ID == creator.receipt.Operation.ID || found && op.ID == latest.Operation.ID || op.TaskID != "" && (op.TaskID == creator.receipt.Operation.TaskID || found && op.TaskID == latest.Operation.TaskID) || op.SubmittedAt != nil && (verified == nil || op.SubmittedAt.Before(verified)) {
		return errConfigurationStale
	}
	dependents, err := siteRetiredDependentCohorts(state, op, parts[0], parts[3])
	if err != nil {
		return err
	}
	for _, row := range state.ActionApplications {
		if row.Resource == creator.receipt.Resource || found && row.Resource == latest.Resource || dependents.rows[siteReceiptDigest(row)] {
			continue
		}
		if row.Operation.TargetID == op.TargetID || row.Operation.ParentID == op.TargetID {
			return errConfigurationOwned
		}
	}
	for _, row := range state.CredentialApplications {
		if row.TargetID == op.TargetID {
			return errConfigurationOwned
		}
	}
	return nil
}

// Observation only. No absence result, even with task success, is a retirement
// receipt or permission to erase ownership. Public routing and atomic settlement
// must be supplied by the existing shared journal lifecycle before activation.
func (a *adapter) observeSiteRetirementTask(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, expected *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if !siteRetirementShape(config) || source == nil || !siteRetirementKnownTask(expected) || expected.SubmittedAt.After(now) {
		return false, errConfigurationTransition
	}
	config = config.DeepCopy()
	expected = expected.DeepCopy()
	inputSource := source
	source = freezeSiteRetirementSource(source)
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, expected) || expected.ControllerGeneration != nc.Generation || expected.SourceUID != string(source.uid) || expected.SourceVersion != source.version {
		return false, errConfigurationStale
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
	if err != nil {
		return false, err
	}
	inputSource.secrets = source.secrets
	parts := strings.Split(strings.TrimPrefix(expected.Resource, siteRetirementPrefix), ":")
	if comparison.keyID != parts[3] {
		return false, errConfigurationSecret
	}
	desired, _, _, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil {
		return false, err
	}
	for _, s := range desired {
		if sitePathDigest(s.path) == parts[1] {
			return false, errConfigurationStale
		}
	}
	if err := siteRetirementLineage(nc.Status.Configuration, config, expected); err != nil {
		return false, err
	}
	dependents, err := siteRetiredDependentCohorts(nc.Status.Configuration, expected, parts[0], parts[3])
	if err != nil {
		return false, err
	}
	for _, want := range desired {
		if dependents.paths[sitePathDigest(want.path)] {
			return false, errConfigurationStale
		}
	}
	history, err := siteRetirementHistory(nc.Status.Configuration)
	if err != nil {
		return false, err
	}
	guard := func() error {
		if err := comparison.check(ctx); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, expected) {
			return errConfigurationStale
		}
		if err := siteRetirementLineage(current.Status.Configuration, config, expected); err != nil {
			return err
		}
		digest, err := siteRetirementHistory(current.Status.Configuration)
		if err != nil {
			return err
		}
		if digest != history {
			return errConfigurationStale
		}
		return nil
	}
	if err := guard(); err != nil {
		return false, err
	}
	completed, err := a.client.configurationTaskTreeCompletion(ctx, expected.TaskID, expected.SubmittedAt.Time, now)
	if err != nil || completed.IsZero() || completed.After(now) {
		return false, err
	}
	for i := 0; i < 2; i++ {
		if err := guard(); err != nil {
			return false, err
		}
		actual, err := a.client.ListSites(ctx)
		if err != nil {
			return false, err
		}
		if err := dependents.checkAbsent(actual); err != nil {
			return false, err
		}
		present := false
		for _, s := range actual {
			if sitePathDigest(s.NameHierarchy) == parts[1] {
				if s.ID != expected.TargetID || s.ParentID != expected.ParentID || s.Type != parts[0] {
					return false, errConfigurationOwned
				}
				present = true
			} else if s.ID == expected.TargetID {
				return false, errConfigurationOwned
			}
		}
		if present {
			return false, nil
		}
		ancestry, err := siteRetirementAncestry(actual, expected.ParentID, parts[0])
		if err != nil {
			return false, err
		}
		base := siteRetirementPrefix + strings.Join(parts[:7], ":")
		mac, err := siteRetirementAncestryMAC(comparison, base, ancestry)
		if err != nil {
			return false, err
		}
		if !hmac.Equal([]byte(mac), []byte(parts[7])) {
			return false, errConfigurationOwned
		}
		if err := guard(); err != nil {
			return false, err
		}
	}
	final, err := a.client.configurationTaskTreeCompletion(ctx, expected.TaskID, expected.SubmittedAt.Time, now)
	if err != nil {
		return false, err
	}
	if !final.Equal(completed) {
		return false, errConfigurationStale
	}
	if err := guard(); err != nil {
		return false, err
	}
	return true, nil
}
