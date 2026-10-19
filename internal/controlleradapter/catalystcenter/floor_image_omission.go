// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const floorImageOmittedPrefix = "floor-image-retained:v1:"

// Retag the same bounded slot. The original operation, acknowledgement and
// verification timestamp remain intact; no native Delete is represented.
type floorImageOmission struct {
	Key                  string `json:"k"`
	Generation           int64  `json:"g"`
	ControllerGeneration int64  `json:"c"`
	SourceUID            string `json:"u"`
	SourceVersion        string `json:"v"`
	Observed             int64  `json:"t"`
}

func floorImageSourceLater(g int64, uid, version string, oldG int64, oldUID, oldVersion string) bool {
	if (uid == "") != (version == "") || g < oldG {
		return false
	}
	return g > oldG || uid != "" && uid == oldUID && version != oldVersion
}

func (w floorImageOmission) resource() string {
	raw, _ := json.Marshal(w)
	return floorImageOmittedPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func floorImageRowKey(row cvk.NetworkControllerConfigurationActionApplication, now time.Time) (string, *floorImageOmission, error) {
	if row.Section != "sites" {
		return "", nil, nil
	}
	key := row.Resource
	var witness *floorImageOmission
	if strings.HasPrefix(key, "floor-image-retained:") {
		if !strings.HasPrefix(key, floorImageOmittedPrefix) || len(key) > 512 {
			return "", nil, errConfigurationOwned
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, floorImageOmittedPrefix))
		var w floorImageOmission
		if err != nil || json.Unmarshal(raw, &w) != nil || w.resource() != key || w.Generation < 1 || w.ControllerGeneration < 1 || w.Observed < 1 || w.Observed > now.Unix() || row.VerifiedAt == nil || w.Observed < row.VerifiedAt.Unix() || row.Operation.ConfigGeneration < 1 || row.Operation.ControllerGeneration < 1 || w.ControllerGeneration < row.Operation.ControllerGeneration || !floorImageSourceLater(w.Generation, w.SourceUID, w.SourceVersion, row.Operation.ConfigGeneration, row.Operation.SourceUID, row.Operation.SourceVersion) {
			return "", nil, errConfigurationOwned
		}
		key, witness = w.Key, &w
	}
	if !strings.HasPrefix(key, "floor-image:") {
		if isFloorImageOperation(row.Operation.Section, row.Operation.Resource) {
			return "", nil, errConfigurationOwned
		}
		return "", nil, nil
	}
	parts := strings.Split(key, ":")
	if len(parts) != 2 || !validTemplateComparisonDigest(parts[1]) {
		return "", nil, errConfigurationOwned
	}
	originalKey, err := floorImageReceiptKey(row.Operation.Resource)
	if err != nil || originalKey != key {
		return "", nil, errConfigurationOwned
	}
	return key, witness, nil
}

func floorImageNewDeclaration(want floorImageIntent, rows []cvk.NetworkControllerConfigurationActionApplication, next *cvk.NetworkControllerConfigurationOperation, now time.Time) error {
	for _, row := range rows {
		key, w, err := floorImageRowKey(row, now)
		if err != nil {
			return err
		}
		if key != want.resource() || w == nil {
			continue
		}
		if next == nil || next.ConfigName != row.Operation.ConfigName || next.ConfigUID != row.Operation.ConfigUID || next.ControllerGeneration < w.ControllerGeneration || !floorImageSourceLater(next.ConfigGeneration, next.SourceUID, next.SourceVersion, w.Generation, w.SourceUID, w.SourceVersion) || next.ConfigGeneration == row.Operation.ConfigGeneration && next.SourceUID == row.Operation.SourceUID && next.SourceVersion == row.Operation.SourceVersion || next.ID != "" && next.ID == row.Operation.ID || next.TaskID != "" && next.TaskID == row.Operation.TaskID || next.SubmittedAt != nil && next.SubmittedAt.Unix() < w.Observed {
			return errConfigurationStale
		}
	}
	return nil
}

func (a *adapter) finalizeFloorImageOmissions(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *sitePlannedAPExecutor) error {
	if config == nil || source == nil || e == nil || e.images == nil || config.DeletionTimestamp != nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "sites" {
		return errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || !reflect.DeepEqual(nc.Status.Configuration.ActionApplications, e.images.receipts) {
		return errConfigurationStale
	}
	owned := false
	for _, owner := range nc.Status.Configuration.Owners {
		owned = owned || owner.Section == "sites" && owner.ConfigName == config.Name && owner.ConfigUID == string(config.UID)
	}
	if !owned {
		return errConfigurationOwned
	}
	wanted := map[string]bool{}
	for _, image := range e.images.desired {
		wanted[image.resource()] = true
	}
	// Source selection is not a declaration omission. Resolve an existing
	// receipt's exact floor before deciding whether this revision controls its
	// omission lifecycle; excluded custody must remain byte-for-byte unchanged.
	var selectionFloors map[string]Site
	working := nc.Status.Configuration.DeepCopy()
	now, changed := time.Now().UTC(), false
	seen := map[string]bool{}
	for i, row := range working.ActionApplications {
		key, oldWitness, err := floorImageRowKey(row, now)
		if err != nil {
			return err
		}
		if key == "" {
			continue
		}
		if seen[key] {
			return errConfigurationOwned
		}
		seen[key] = true
		if wanted[key] || oldWitness != nil || row.Operation.ConfigName != config.Name || row.Operation.ConfigUID != string(config.UID) {
			continue
		}
		if err := validateFloorImageReceipt(row, key, row.Operation.TargetID, config.Name, string(config.UID), now); err != nil {
			return err
		}
		if source.siteSelection != nil {
			if selectionFloors == nil {
				actual, err := a.client.ListSites(ctx)
				if err != nil {
					return err
				}
				selectionFloors = map[string]Site{}
				for _, floor := range actual {
					selectionFloors[floor.ID] = floor
				}
			}
			floor := selectionFloors[row.Operation.TargetID]
			if floor.Type != "floor" || (floorImageIntent{floor: floor.NameHierarchy}).resource() != key {
				return errConfigurationOwned
			}
			allowed, err := source.siteSelection.allows(floor.NameHierarchy)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
		}
		if !floorImageSourceLater(config.Generation, string(source.uid), source.version, row.Operation.ConfigGeneration, row.Operation.SourceUID, row.Operation.SourceVersion) {
			return errConfigurationStale
		}
		w := floorImageOmission{Key: key, Generation: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, Observed: now.Unix()}
		working.ActionApplications[i].Resource = w.resource()
		if _, _, err := floorImageRowKey(working.ActionApplications[i], now); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	// Existing native and private custody checks precede the bookkeeping CAS.
	actual, err := a.client.ListSites(ctx)
	if err != nil {
		return err
	}
	if err := e.sites.checkCreatorIdentities(actual); err != nil {
		return err
	}
	if err := e.sites.checkPrivateBaselines(ctx, actual); err != nil {
		return err
	}
	byID := map[string]Site{}
	for _, site := range actual {
		byID[site.ID] = site
	}
	for _, row := range working.ActionApplications {
		key, w, err := floorImageRowKey(row, now)
		if err != nil {
			return err
		}
		if w == nil || wanted[key] || row.Operation.ConfigUID != string(config.UID) {
			continue
		}
		floor := byID[row.Operation.TargetID]
		if floor.Type != "floor" || (floorImageIntent{floor: floor.NameHierarchy}).resource() != key {
			return errConfigurationOwned
		}
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc.Status.Configuration = working
	// This path has no active operation: use the existing guarded status CAS.
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if stored.Status.Configuration == nil || stored.Status.Configuration.Operation != nil || !reflect.DeepEqual(stored.Status.Configuration.ActionApplications, working.ActionApplications) {
		return errConfigurationStale
	}
	return a.checkConfigurationSnapshot(ctx, config, source)
}
