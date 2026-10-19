// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Recognize the existing definition/tag and profile/site association spellings.
// Switching definitions retain their original project-aware dependency binding;
// physical work remains outside this metadata-only observer.
func networkProfileMetadataResource(resource string) (kind, selector string, ok bool) {
	if switchingCreateSelected(resource) {
		key, ok := switchingCreateKey(resource)
		return "switching-create", key, ok
	}
	if local, found := strings.CutPrefix(resource, "profiles/"); found {
		return "switching-profile", local, validProfileName(local)
	}
	if wirelessTagPrivateSelected(resource) {
		canonical, _, _, valid := wirelessTagPrivateParts(resource)
		if !valid {
			return "", "", false
		}
		return networkProfileMetadataResource(canonical)
	}
	digest := func(value string) bool {
		_, err := hex.DecodeString(value)
		return len(value) == 64 && value == strings.ToLower(value) && err == nil
	}
	for prefix, kind := range map[string]string{"wireless-sites/": "wireless-assignment", "sites/": "switching-assignment"} {
		if local, found := strings.CutPrefix(resource, prefix); found {
			if !strings.Contains(local, "/") && (strings.HasPrefix(local, profileSiteAddFamily) || strings.HasPrefix(local, profileSiteRemoveFamily)) {
				if _, _, _, ok := profileSiteApplicationParts(prefix + local); ok {
					return kind, local, true
				}
				return "", "", false
			}
			at := strings.LastIndex(local, "/")
			if at > 0 && validProfileName(local[:at]) && digest(local[at+1:]) {
				return kind, local, true
			}
			return "", "", false
		}
	}
	if local, found := strings.CutPrefix(resource, "wireless/wireless-profile:"); found {
		at := strings.LastIndex(local, ":")
		if at > 0 && validProfileName(local[:at]) && digest(local[at+1:]) {
			return "profile", local[:at], true
		}
		return "", "", false
	}
	for prefix, kind := range map[string]string{"wireless-site-tags/site-tag:": "site-tag", "wireless-policy-tags/policy-tag:": "policy-tag"} {
		if local, found := strings.CutPrefix(resource, prefix); found {
			parts := strings.Split(local, ":")
			if len(parts) == 2 && digest(parts[0]) && digest(parts[1]) {
				return kind, parts[0], true
			}
			return "", "", false
		}
	}
	if local, found := strings.CutPrefix(resource, "wireless-policy-tags/"+wirelessPolicyCohortPrefix); found && digest(local) {
		return "policy-cohort", "", true
	}
	return "", "", false
}

func networkProfileMetadataKindSupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Section != "network_profiles" || !validAPIID(op.ParentID) {
		return false
	}
	kind, _, ok := networkProfileMetadataResource(op.Resource)
	if kind == "switching-create" {
		return ok && validSwitchingCreateOperation(op)
	}
	if kind == "switching-profile" && !validSwitchingProfileBinding(op.ParentID) && !legacyEmptySwitchingProfileBinding(op.ParentID) {
		return false
	}
	if wirelessTagPruneSelected(op.Resource) {
		return ok && op.Action == "Delete" && validAPIID(op.TargetID)
	}
	if kind == "wireless-assignment" || kind == "switching-assignment" {
		return ok && validAPIID(op.TargetID) && (op.Action == "Update" && !profileSiteRemovalSelected(op) || op.Action == "Delete" && profileSiteRemovalSelected(op))
	}
	return ok && (op.Action == "Create" && op.TargetID == "" || kind != "policy-cohort" && op.Action == "Update" && validAPIID(op.TargetID))
}

func networkProfileMetadataEnvelope(op *cvk.NetworkControllerConfigurationOperation) bool {
	if !networkProfileMetadataKindSupported(op) || op.Phase != "Unknown" && op.Phase != "Accepted" {
		return false
	}
	// Reuse the common metadata-only journal validation, with this family's
	// explicitly required parent identity checked above rather than discarded.
	copy := op.DeepCopy()
	copy.ParentID = ""
	if profileSiteRemovalSelected(op) || wirelessTagPruneSelected(op.Resource) {
		copy.Action = "Update"
	}
	copy.Phase = "Unknown"
	return definitionCreateKnownTaskRecoverySupported(copy, "network_profiles") || definitionUpdateKnownTaskRecoverySupported(copy, "network_profiles")
}

func (e *networkProfileConfiguration) selectsProfileMetadata(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op != nil && wirelessTagPruneSelected(op.Resource) {
		return networkProfileMetadataEnvelope(op) && e.tagPrune != nil && e.tagPrune.selected(op)
	}
	if op != nil && wirelessTagPrivateSelected(op.Resource) {
		copy := op.DeepCopy()
		copy.Resource = wirelessTagCanonical(op.Resource)
		op = copy
	}
	if !networkProfileMetadataEnvelope(op) {
		return false
	}
	kind, selector, _ := networkProfileMetadataResource(op.Resource)
	if kind == "switching-create" {
		_, _, ok := e.switchingCreatorSelection(op)
		return ok
	}
	if kind == "switching-profile" {
		_, ok := e.selectedSwitchingProfile(selector)
		return ok
	}
	if kind == "wireless-assignment" || kind == "switching-assignment" {
		for _, part := range e.parts {
			if p, ok := part.executor.(*profileSiteExecutor); ok && p.wireless == (kind == "wireless-assignment") {
				local := op.DeepCopy()
				local.Resource = selector
				if _, err := p.selectedPair(local); err == nil {
					return true
				}
			}
		}
		return false
	}
	if kind == "profile" {
		for _, part := range e.parts {
			if p, ok := part.executor.(*wirelessProfileExecutor); ok && part.name == "wireless" {
				for _, want := range p.desired {
					if want.name == selector {
						return true
					}
				}
			}
		}
		return false
	}
	for _, tags := range e.tagExecutors {
		if tags.policy != (kind != "site-tag") {
			continue
		}
		for _, p := range tags.desired {
			count := 0
			for _, tag := range tags.tags(p) {
				if len(tag.sites) == 0 {
					continue
				}
				count++
				identity, err := wirelessTagDigest([]string{p.name, tag.name})
				if err == nil && identity == selector && kind != "policy-cohort" {
					return true
				}
			}
			if kind == "policy-cohort" && count >= 2 {
				return true
			}
		}
	}
	return false
}

// Keep native snapshots in memory only. Normal verification rechecks the exact
// dependency resource, parent/target identities and full desired readback.
func (e *networkProfileConfiguration) observeProfileMetadata(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (any, bool, error) {
	if op != nil && wirelessTagBindingMode(op.Resource) != "" && !wirelessTagPruneSelected(op.Resource) {
		return e.boundTagReadback(ctx, op)
	}
	if op != nil && wirelessTagPruneSelected(op.Resource) {
		if e.tagPrune == nil {
			return nil, false, errConfigurationOwned
		}
		return e.tagPrune.observe(ctx, op)
	}
	if op != nil && wirelessTagPrivateSelected(op.Resource) {
		copy := op.DeepCopy()
		copy.Resource = wirelessTagCanonical(op.Resource)
		op = copy
	}
	if !e.selectsProfileMetadata(op) {
		return nil, false, errConfigurationTransition
	}
	kind, selector, _ := networkProfileMetadataResource(op.Resource)
	if kind == "switching-create" {
		_, name, ok := e.switchingCreatorSelection(op)
		if !ok {
			return nil, false, errConfigurationTransition
		}
		local := op.DeepCopy()
		local.Resource = "profiles/" + name
		return e.observeSwitchingProfileMetadata(ctx, local, name)
	}
	if kind == "switching-profile" {
		return e.observeSwitchingProfileMetadata(ctx, op, selector)
	}
	if kind == "wireless-assignment" || kind == "switching-assignment" {
		for _, part := range e.parts {
			if p, ok := part.executor.(*profileSiteExecutor); ok && p.wireless == (kind == "wireless-assignment") {
				local := op.DeepCopy()
				local.Resource = selector
				selected, err := p.selectedPair(local)
				if err != nil {
					continue
				}
				observed, err := selected.observe(ctx)
				if err != nil {
					return nil, false, err
				}
				done, err := profileSiteObservationMatches(observed, local)
				return observed, done, err
			}
		}
		return nil, false, errConfigurationTransition
	}
	if kind == "profile" {
		for _, part := range e.parts {
			if p, ok := part.executor.(*wirelessProfileExecutor); ok && part.name == "wireless" {
				inventory, err := p.client.wirelessProfileInventory(ctx)
				if err != nil {
					return nil, false, err
				}
				done, err := e.verify(ctx, op)
				return inventory[selector], done, err
			}
		}
	}
	for _, tags := range e.tagExecutors {
		if tags.policy != (kind != "site-tag") {
			continue
		}
		rows, err := tags.observe(ctx)
		if err != nil {
			return nil, false, err
		}
		selected := []wirelessProfileTagObservation{}
		for _, row := range rows {
			if kind == "policy-cohort" && row.profileID == op.ParentID || strings.HasSuffix(op.Resource, "/"+row.resource) {
				selected = append(selected, row)
			}
		}
		done, err := e.verify(ctx, op)
		return selected, done, err
	}
	return nil, false, errConfigurationTransition
}

func (a *adapter) bindNetworkProfileTaskRecovery(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) {
	if plan == nil || plan.section != "network_profiles" {
		return
	}
	e, ok := plan.executor.(*networkProfileConfiguration)
	if !ok {
		return
	}
	previousRecovery, previousCompletion := plan.recoverKnownTask, plan.observeCompletion
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return e.selectsProfileMetadata(op) && op.Phase == "Unknown" || previousRecovery != nil && previousRecovery(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if !e.selectsProfileMetadata(op) {
			if previousCompletion != nil {
				return previousCompletion(ctx, op, now)
			}
			if op == nil || op.SubmittedAt == nil {
				return false, errConfigurationTransition
			}
			return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		return a.observeOwnedProfileMetadataTask(ctx, config, source, op, now, e.observeProfileMetadata)
	}
}

func (a *adapter) observeOwnedProfileMetadataTask(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, now time.Time, observe func(context.Context, *cvk.NetworkControllerConfigurationOperation) (any, bool, error)) (bool, error) {
	return a.observeOwnedConfigurationOperation(ctx, config, source, op, "network_profiles", func(ctx context.Context) (bool, error) {
		// Preserve the ordinary Accepted -> Verifying boundary: partial
		// readback belongs to verification, not task-running status. Unknown
		// recovery additionally needs stable native convergence below.
		if op.Phase == "Accepted" {
			done, err := a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
			if err != nil || !done {
				return false, err
			}
			return a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		before, beforeDone, err := observe(ctx, op)
		if err != nil {
			return false, err
		}
		done, err := a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil || !done {
			return false, err
		}
		done, err = a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil || !done {
			return false, err
		}
		after, afterDone, err := observe(ctx, op)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(before, after) {
			return false, errConfigurationStale
		}
		return beforeDone && afterDone, nil
	})
}
