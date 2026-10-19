// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const siteRetirementPrefix = "site-retire:v2:"

// Read-only authority binding. Submission remains a separate guarded writer
// through the shared journal and retained completion lifecycle.
type siteRetirementCandidate struct {
	resource string
	identity configurationResourceIdentity
	check    func(context.Context) error
}

func (*siteRetirementCandidate) String() string   { return "<private site retirement candidate>" }
func (*siteRetirementCandidate) GoString() string { return "<private site retirement candidate>" }

func siteRetirementOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || !strings.HasPrefix(op.Resource, siteRetirementPrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(op.Resource, siteRetirementPrefix), ":")
	if len(op.Resource) > 512 || len(parts) != 8 || parts[0] != "area" && parts[0] != "building" && parts[0] != "floor" || parts[2] != "i" && parts[2] != "c" {
		return false
	}
	for _, i := range []int{1, 3, 4, 5, 6, 7} {
		if !validTemplateComparisonDigest(parts[i]) {
			return false
		}
	}
	source := parts[2] == "i" && op.SourceUID == "" && op.SourceVersion == "" || parts[2] == "c" && op.SourceUID != "" && op.SourceVersion != ""
	return (op.TaskID == "" || validAPIID(op.TaskID)) && source && op.Section == "sites" && op.Action == "Delete" && canonicalTemplateUUID(op.TargetID) && validAPIID(op.ParentID) && op.TargetID != op.ParentID && op.TargetSerial == "" && op.CreatedResourceID == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.PreparedAt == nil && op.CancellationRequestedAt == nil && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
}

// A separate domain binds the full retirement selector, including predecessor
// receipt digests. Existing creator/update MAC domains remain unchanged.
func siteRetirementMAC(comparison *templateInputComparison, base string, native []byte) (string, error) {
	return siteRetirementFingerprint(comparison, base, native, false)
}

func siteRetirementAncestryMAC(comparison *templateInputComparison, base string, native []byte) (string, error) {
	return siteRetirementFingerprint(comparison, base, native, true)
}

func siteRetirementFingerprint(comparison *templateInputComparison, base string, native []byte, ancestry bool) (string, error) {
	parts := strings.Split(strings.TrimPrefix(base, siteRetirementPrefix), ":")
	count, domain := 6, "cvk-site-retirement-v2"
	if ancestry {
		count, domain = 7, "cvk-site-retirement-ancestry-v1"
	}
	if !strings.HasPrefix(base, siteRetirementPrefix) || len(parts) != count || (parts[0] != "area" && parts[0] != "building" && parts[0] != "floor") || (parts[2] != "i" && parts[2] != "c") {
		return "", errConfigurationTransition
	}
	for _, i := range []int{1, 3, 4, 5} {
		if !validTemplateComparisonDigest(parts[i]) {
			return "", errConfigurationTransition
		}
	}
	if ancestry && !validTemplateComparisonDigest(parts[6]) {
		return "", errConfigurationTransition
	}
	if comparison == nil || len(comparison.key) != 32 || comparison.keyID != parts[3] || comparison.check == nil {
		return "", errConfigurationSecret
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte(domain + "\x00" + base + "\x00"))
	mac.Write(native)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func siteRetirementHistory(state *cvk.NetworkControllerConfigurationStatus) (string, error) {
	if state == nil {
		return "", errConfigurationOwned
	}
	copy := state.DeepCopy()
	copy.Operation = nil
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", errConfigurationOwned
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func siteRetirementShape(config *configapi.NetworkControllerConfig) bool {
	if !sitePruneApplyShape(config) {
		return false
	}
	// Validate the complete product shape under ordinary Retain admission;
	// the private site lifecycle supplies the additional prune authority.
	staged := config.DeepCopy()
	staged.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	return supportedConfigurationApply(staged)
}

// Copy the mutable site model/asset bytes while retaining the established
// snapshot metadata. Projection authorization is resolved anew by each caller.
func freezeSiteRetirementSource(source *configSourceSnapshot) *configSourceSnapshot {
	frozen := *source
	frozen.raw = bytes.Clone(source.raw)
	frozen.siteSelection = source.siteSelection.clone()
	frozen.floorImages = map[string][]byte{}
	for name, content := range source.floorImages {
		frozen.floorImages[name] = bytes.Clone(content)
	}
	return &frozen
}

func siteRetirementPrepared(op *cvk.NetworkControllerConfigurationOperation, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, targetID string) bool {
	return op != nil && siteRetirementOperation(op) && op.Phase == "Prepared" && configurationOperationID.MatchString(op.ID) && op.TaskID == "" && op.SubmittedAt == nil && op.ConfigName == config.Name && op.ConfigUID == string(config.UID) && op.ConfigGeneration == config.Generation && op.ControllerGeneration == nc.Generation && op.SourceUID == string(source.uid) && op.SourceVersion == source.version && op.TargetID == targetID
}

func (a *adapter) bindSiteRetirement(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, targetID string) (*siteRetirementCandidate, error) {
	if nc == nil || source == nil || !canonicalTemplateUUID(targetID) || !siteRetirementShape(config) || nc.Status.Configuration == nil {
		return nil, errConfigurationTransition
	}
	prepared := nc.Status.Configuration.Operation.DeepCopy()
	if prepared != nil && !siteRetirementPrepared(prepared, nc, config, source, targetID) {
		return nil, errConfigurationTransition
	}
	// Own the parsed source and authority metadata; later caller edits cannot
	// change what this candidate represents. Keys stay in the existing projection.
	config = config.DeepCopy()
	nc = nc.DeepCopy()
	inputSource := source
	source = freezeSiteRetirementSource(source)
	comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
	if err != nil {
		return nil, err
	}
	// The common prepare/start journal consumes this existing projection
	// authorization too. Only its snapshot is shared, never authority metadata.
	inputSource.secrets = source.secrets
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, config); err != nil {
		return nil, err
	}
	desired, _, _, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil {
		return nil, err
	}
	creators, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil {
		return nil, err
	}
	applied, err := siteAppliedRecords(nc.Status.Configuration)
	if err != nil {
		return nil, err
	}
	actual, err := a.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var target Site
	for _, s := range actual {
		if s.ID == targetID {
			target = s
		}
	}
	if target.ID == "" || target.NameHierarchy == "Global" {
		return nil, errConfigurationOwned
	}
	allowed, err := source.siteSelection.allows(target.NameHierarchy)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, errConfigurationTransition
	}
	var creator siteCreatorRecord
	for resource, row := range creators {
		if row.id == targetID {
			if _, wanted := siteDesiredOperation(desired, resource); wanted {
				return nil, errConfigurationTransition
			}
			creator = row
		}
	}
	if creator.id == "" || creator.keyID != comparison.keyID || creator.kind != target.Type || creator.receipt.Operation.ParentID != target.ParentID || siteResourcePathDigest(creator.receipt.Operation.Resource) != sitePathDigest(target.NameHierarchy) || creator.receipt.Operation.ConfigName != config.Name || creator.receipt.Operation.ConfigUID != string(config.UID) {
		return nil, errConfigurationOwned
	}
	latest, hasLatest := applied[sitePathDigest(target.NameHierarchy)]
	previous := &creator.receipt.Operation
	if hasLatest {
		if len(latest.EffectReceipts) != 0 {
			return nil, errConfigurationOwned
		}
		previous = &latest.Operation
	}
	frame := &cvk.NetworkControllerConfigurationOperation{ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, TargetID: targetID, ParentID: target.ParentID}
	if frame.ConfigGeneration < previous.ConfigGeneration || frame.ControllerGeneration < previous.ControllerGeneration || !configurationInputAfter(frame, previous) {
		return nil, errConfigurationStale
	}
	dependents, err := siteRetiredDependentCohorts(nc.Status.Configuration, frame, target.Type, comparison.keyID)
	if err != nil {
		return nil, err
	}
	if err := dependents.checkAbsent(actual); err != nil {
		return nil, err
	}
	for _, want := range desired {
		if dependents.paths[sitePathDigest(want.path)] {
			return nil, errConfigurationStale
		}
	}
	// Other retained effects need their own retirement protocol. In particular,
	// do not orphan image, AP, credential or cross-section producer evidence.
	for _, row := range nc.Status.Configuration.ActionApplications {
		if row.Resource == creator.receipt.Resource || hasLatest && row.Resource == latest.Resource || dependents.rows[siteReceiptDigest(row)] {
			continue
		}
		if row.Operation.TargetID == targetID || row.Operation.ParentID == targetID {
			return nil, errConfigurationOwned
		}
	}
	for _, row := range nc.Status.Configuration.CredentialApplications {
		if row.TargetID == targetID {
			return nil, errConfigurationOwned
		}
	}
	e := &siteConfigurationExecutor{client: a.client, desired: []desiredSite{{kind: target.Type, path: target.NameHierarchy}}, creators: creators, applied: applied, creatorOwner: string(config.UID), comparison: comparison}
	if err := e.checkCreatorIdentities(actual); err != nil {
		return nil, err
	}
	if err := e.checkPrivateBaselines(ctx, actual); err != nil {
		return nil, err
	}
	initial, err := a.client.observeUnusedSiteLeaf(ctx, target)
	if err != nil {
		return nil, err
	}
	history, err := siteRetirementHistory(nc.Status.Configuration)
	if err != nil {
		return nil, err
	}
	mode := "i"
	if source.uid != "" {
		mode = "c"
	}
	prior := strings.Repeat("0", 64)
	if hasLatest {
		prior = siteReceiptDigest(latest)
	}
	base := siteRetirementPrefix + strings.Join([]string{target.Type, sitePathDigest(target.NameHierarchy), mode, comparison.keyID, siteReceiptDigest(creator.receipt), prior}, ":")
	mac, err := siteRetirementMAC(comparison, base, initial.native)
	if err != nil {
		return nil, err
	}
	ancestry, err := siteRetirementAncestry(actual, target.ParentID, target.Type)
	if err != nil {
		return nil, err
	}
	// The recoverable ancestry proof also authenticates the pre-Delete state
	// MAC. Neither field can be edited independently after the object is gone.
	stateBase := base + ":" + mac
	ancestryMAC, err := siteRetirementAncestryMAC(comparison, stateBase, ancestry)
	if err != nil {
		return nil, err
	}
	resource, identity := stateBase+":"+ancestryMAC, configurationResourceIdentity{target: target.ID, parent: target.ParentID}
	candidate := &siteRetirementCandidate{resource: resource, identity: identity}
	checkHistory := func(state *cvk.NetworkControllerConfigurationStatus, operation *cvk.NetworkControllerConfigurationOperation) error {
		digest, err := siteRetirementHistory(state)
		if err != nil {
			return err
		}
		if digest != history {
			return errConfigurationStale
		}
		if operation == nil {
			operation = frame
		}
		if _, err := siteRetiredDependentCohorts(state, operation, target.Type, comparison.keyID); err != nil {
			return err
		}
		return nil
	}
	checkJournal := func(ctx context.Context) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "sites", resource, "Delete", identity, siteRetirementOperation, checkHistory)
	}
	candidate.check = func(ctx context.Context) error {
		if candidate.resource != resource || !reflect.DeepEqual(candidate.identity, identity) {
			return errConfigurationStale
		}
		if err := checkJournal(ctx); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			if err := comparison.check(ctx); err != nil {
				return err
			}
			current, err := a.client.ListSites(ctx)
			if err != nil {
				return err
			}
			if err := dependents.checkAbsent(current); err != nil {
				return err
			}
			if err := e.checkCreatorIdentities(current); err != nil {
				return err
			}
			if err := e.checkPrivateBaselines(ctx, current); err != nil {
				return err
			}
			observed, err := a.client.observeUnusedSiteLeaf(ctx, target)
			if err != nil {
				return err
			}
			now, err := siteRetirementMAC(comparison, base, observed.native)
			if err != nil {
				return err
			}
			if !hmac.Equal([]byte(mac), []byte(now)) {
				return errConfigurationStale
			}
			if err := checkJournal(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	// A fresh controller read fences changes occurring during candidate creation.
	current, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || current.DeletionTimestamp != nil || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, prepared) {
		return nil, errConfigurationStale
	}
	if err := checkHistory(current.Status.Configuration, nil); err != nil {
		return nil, err
	}
	if prepared != nil && (prepared.Resource != candidate.resource || prepared.TargetID != identity.target || prepared.ParentID != identity.parent) {
		return nil, errConfigurationStale
	}
	if err := comparison.check(ctx); err != nil {
		return nil, err
	}
	return candidate, nil
}
