// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Reconstruct a single retired-site lifecycle using the existing site executor,
// transport, one-use preflight and journal. Effects with separate cleanup
// contracts remain gated by their retained evidence and native consumers.
// An acknowledged operation returns an observation executor and no writer.
func (a *adapter) siteRecreationExecution(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string) (*siteConfigurationExecutor, *configurationWrite, error) {
	if nc == nil || source == nil || nc.Status.Configuration == nil || !siteRetirementShape(config) || !validSitePath(path) || path == "Global" {
		return nil, nil, errConfigurationTransition
	}
	nc, config = nc.DeepCopy(), config.DeepCopy()
	input := source
	source = freezeSiteRetirementSource(source)
	comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
	if err != nil {
		return nil, nil, err
	}
	input.secrets = source.secrets
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, nil, err
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, config); err != nil {
		return nil, nil, err
	}
	desired, placements, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil {
		return nil, nil, err
	}
	// Effects need their own lifecycle coordination; do not silently ignore them.
	if len(images) != 0 || len(placements) != 0 {
		return nil, nil, errConfigurationTransition
	}
	var want *desiredSite
	for i := range desired {
		if desired[i].path == path {
			want = &desired[i]
		}
	}
	if want == nil {
		return nil, nil, errConfigurationStale
	}
	if err := validateSiteExecution(desired); err != nil {
		return nil, nil, err
	}
	cohorts, err := siteCurrentRetirementCohorts(nc.Status.Configuration)
	if err != nil {
		return nil, nil, err
	}
	cohort, found := cohorts[sitePathDigest(path)]
	if !found || cohort.Kind != want.kind {
		return nil, nil, errConfigurationOwned
	}
	creators, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil {
		return nil, nil, err
	}
	archives, err := siteHistoryCohorts(nc.Status.Configuration)
	if err != nil {
		return nil, nil, err
	}
	mode := "i"
	if source.uid != "" {
		mode = "c"
	}
	resource := siteRecreatePrefix + strings.Join([]string{want.kind, sitePathDigest(path), mode, comparison.keyID, cohort.Retired.Operation.ID, cohort.Digest}, ":")
	parentID := cohort.Retired.Operation.ParentID
	if replacement, found := siteCreatorForResource(creators, parentSitePath(path)); found && replacement.id != parentID {
		resource = siteRecreateHandoffPrefix + strings.TrimPrefix(resource, siteRecreatePrefix) + ":" + siteReceiptDigest(replacement.receipt)
		parentID = replacement.id
	}
	frame := &cvk.NetworkControllerConfigurationOperation{Section: "sites", Resource: resource, Action: "Create", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, ParentID: parentID}
	active := nc.Status.Configuration.Operation.DeepCopy()
	recovering := false
	if active != nil {
		if !siteSingularOperation(active) || active.Resource != resource || active.ConfigName != frame.ConfigName || active.ConfigUID != frame.ConfigUID || active.ConfigGeneration != frame.ConfigGeneration || active.ControllerGeneration != frame.ControllerGeneration || active.SourceUID != frame.SourceUID || active.SourceVersion != frame.SourceVersion || active.ParentID != frame.ParentID || !configurationOperationID.MatchString(active.ID) {
			return nil, nil, errConfigurationStale
		}
		if active.Phase == "Prepared" {
			if active.TaskID != "" || active.SubmittedAt != nil {
				return nil, nil, errConfigurationTransition
			}
		} else {
			recovering = (active.Phase == "Accepted" || active.Phase == "Unknown" || active.Phase == "Verifying") && validAPIID(active.TaskID) && active.SubmittedAt != nil && !active.SubmittedAt.IsZero()
			if !recovering {
				return nil, nil, errConfigurationBusy
			}
		}
		frame = active
	}
	if _, err := siteRecreationAuthority(nc.Status.Configuration, frame); err != nil {
		return nil, nil, err
	}
	replacement, err := siteRecreationParentProof(nc.Status.Configuration, frame, cohort, archives, true)
	if err != nil {
		return nil, nil, err
	}
	var parentBaseline *siteConfigurationExecutor
	if replacement != nil {
		parentBaseline = &siteConfigurationExecutor{client: a.client, desired: []desiredSite{{kind: replacement.kind, path: parentSitePath(path)}}, creators: creators, creatorOwner: string(config.UID), comparison: comparison}
	}
	if err := siteCreateCapacity(nc.Status.Configuration, frame); err != nil {
		return nil, nil, err
	}
	history, err := siteRetirementHistory(nc.Status.Configuration)
	if err != nil {
		return nil, nil, err
	}
	applied, err := siteAppliedRecords(nc.Status.Configuration)
	if err != nil {
		return nil, nil, err
	}
	if parentBaseline != nil {
		parentBaseline.applied = applied
	}
	retiredIDs := map[string]bool{cohort.ID: true}
	for _, archived := range archives {
		if archived.Path == cohort.Path || replacement != nil && archived.Path == sitePathDigest(parentSitePath(path)) {
			retiredIDs[archived.ID] = true
		}
	}
	e := &siteConfigurationExecutor{client: a.client, desired: []desiredSite{*want}, creators: creators, applied: applied, creatorOwner: string(config.UID), comparison: comparison}
	e.privateResource = func(d desiredSite) string {
		if d.path == path && d.kind == want.kind {
			return resource
		}
		return ""
	}
	checkHistory := func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
		if _, err := siteRecreationAuthority(state, op); err != nil {
			return err
		}
		digest, err := siteRetirementHistory(state)
		if err != nil {
			return err
		}
		if digest != history {
			return errConfigurationStale
		}
		return siteCreateCapacity(state, op)
	}
	checkNative := func(actual []Site, absent bool) error {
		// Absence of the old UUID anywhere is required, not just at its old path.
		for _, s := range actual {
			if retiredIDs[s.ID] || absent && (s.NameHierarchy == path || strings.HasPrefix(s.NameHierarchy, path+"/")) {
				return errConfigurationOwned
			}
		}
		ancestry, err := siteRetirementAncestry(actual, frame.ParentID, want.kind)
		if err != nil {
			return err
		}
		if parentBaseline != nil {
			if err := parentBaseline.checkCreatorIdentities(actual); err != nil {
				return err
			}
			return parentBaseline.checkPrivateBaselines(ctx, actual)
		}
		parts := strings.Split(strings.TrimPrefix(cohort.Retired.Operation.Resource, siteRetirementPrefix), ":")
		mac, err := siteRetirementAncestryMAC(comparison, siteRetirementPrefix+strings.Join(parts[:7], ":"), ancestry)
		if err != nil {
			return err
		}
		if !hmac.Equal([]byte(mac), []byte(parts[7])) {
			return errConfigurationOwned
		}
		return nil
	}
	e.submissionGuard = func(ctx context.Context, selected, action string, identity configurationResourceIdentity) error {
		if selected != resource || action != "Create" || identity.target != "" || identity.parent != frame.ParentID {
			return errConfigurationStale
		}
		if err := comparison.check(ctx); err != nil {
			return err
		}
		if err := a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "sites", resource, "Create", identity, siteSingularOperation, checkHistory); err != nil {
			return err
		}
		actual, err := a.client.ListSites(ctx)
		if err != nil {
			return err
		}
		return checkNative(actual, true)
	}
	var actual []Site
	for i := 0; i < 2; i++ {
		if err := comparison.check(ctx); err != nil {
			return nil, nil, err
		}
		actual, err = a.client.ListSites(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := checkNative(actual, !recovering); err != nil {
			return nil, nil, err
		}
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, active) {
		return nil, nil, errConfigurationStale
	}
	if err := checkHistory(current.Status.Configuration, frame); err != nil {
		return nil, nil, err
	}
	if err := comparison.check(ctx); err != nil {
		return nil, nil, err
	}
	if recovering {
		return e, nil, nil
	}
	mutation, err := nextSiteMutation(e.desired, actual)
	if err != nil {
		return nil, nil, err
	}
	if mutation == nil || mutation.action != "Create" || mutation.parentID != frame.ParentID {
		return nil, nil, errConfigurationOwned
	}
	write, err := e.singularWrite(ctx, actual, mutation)
	if err != nil {
		return nil, nil, err
	}
	return e, write, nil
}
