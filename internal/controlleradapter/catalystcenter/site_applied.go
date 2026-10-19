// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const sitePrivateUpdatePrefix = "site-private-update:v1:"
const siteAppliedPrefix = "site-applied:v1:"

func sitePrivateOperationParts(resource string) ([]string, bool) {
	if parts, ok := sitePrivateCreationParts(resource); ok {
		return parts, true
	}
	if parts, ok := sitePrivateFloorUpdateParts(resource); ok {
		return parts[:4], true
	}
	if !strings.HasPrefix(resource, sitePrivateUpdatePrefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, sitePrivateUpdatePrefix), ":")
	if len(parts) != 5 || !validTemplateComparisonDigest(parts[4]) {
		return nil, false
	}
	base, ok := sitePrivateCreateParts(sitePrivateCreatePrefix + strings.Join(parts[:4], ":"))
	return base, ok
}

func siteReceiptDigest(row cvk.NetworkControllerConfigurationActionApplication) string {
	// This record contains public identities and keyed fingerprints only.
	canonical := row.DeepCopy()
	sort.Strings(canonical.EffectReceipts)
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func siteAppliedSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	return op != nil && op.Section == "sites" && (strings.HasPrefix(op.Resource, sitePrivateUpdatePrefix) || strings.HasPrefix(op.Resource, sitePrivateFloorUpdatePrefix))
}

func siteCreatorForResource(records map[string]siteCreatorRecord, resource string) (siteCreatorRecord, bool) {
	digest := siteResourcePathDigest(resource)
	if digest == "" {
		return siteCreatorRecord{}, false
	}
	for key, record := range records {
		if siteResourcePathDigest(key) == digest {
			return record, true
		}
	}
	return siteCreatorRecord{}, false
}

func siteApplyCreator(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (siteCreatorRecord, error) {
	if _, err := siteCreatedRecords(state); err != nil {
		return siteCreatorRecord{}, err
	}
	return siteApplyCreatorCurrent(state, op)
}

func siteApplyCreatorCurrent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (siteCreatorRecord, error) {
	if !siteAppliedSelected(op) || !siteSingularOperation(op) || op.Action != "Update" {
		return siteCreatorRecord{}, errConfigurationTransition
	}
	parts, ok := sitePrivateOperationParts(op.Resource)
	creators, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return siteCreatorRecord{}, err
	}
	original, exists := siteCreatorForResource(creators, op.Resource)
	base, private := sitePrivateCreationParts(original.receipt.Operation.Resource)
	if !ok || !exists || !private || !reflect.DeepEqual(parts, base) || op.TargetID != original.id || op.ParentID != original.receipt.Operation.ParentID ||
		op.ConfigUID != original.receipt.Operation.ConfigUID || op.ConfigName != original.receipt.Operation.ConfigName || op.ConfigGeneration < original.receipt.Operation.ConfigGeneration || op.ControllerGeneration < original.receipt.Operation.ControllerGeneration {
		return siteCreatorRecord{}, errConfigurationOwned
	}
	return original, nil
}

func siteAppliedResult(op *cvk.NetworkControllerConfigurationOperation, creator siteCreatorRecord, mac string) string {
	parts, ok := sitePrivateOperationParts(op.Resource)
	if !ok || !validTemplateComparisonDigest(mac) {
		return ""
	}
	result := siteAppliedPrefix + strings.Join(parts[:3], ":") + ":" + op.TargetID + ":" + parts[3] + ":" + mac + ":" + siteReceiptDigest(creator.receipt)
	if floor, ok := sitePrivateFloorUpdateParts(op.Resource); ok {
		result += ":" + floor[5]
	}
	return result
}

func siteAppliedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	if _, err := siteCreatedRecords(state); err != nil {
		return nil, err
	}
	return siteAppliedRecordsCurrent(state)
}

func siteAppliedRecordsCurrent(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out, nil
	}
	counts := map[string]int{}
	for _, row := range state.ActionApplications {
		counts[row.Operation.ID]++
	}
	for _, row := range state.ActionApplications {
		if strings.HasPrefix(row.Resource, "site-history:") {
			continue
		}
		if !strings.HasPrefix(row.Resource, siteAppliedPrefix) && !siteAppliedSelected(&row.Operation) {
			continue
		}
		op := &row.Operation
		creator, err := siteApplyCreatorCurrent(state, op)
		parts := strings.Split(strings.TrimPrefix(row.Resource, siteAppliedPrefix), ":")
		n := 7
		if _, floor := sitePrivateFloorUpdateParts(op.Resource); floor {
			n++
		}
		if err != nil || len(parts) != n {
			return nil, errConfigurationOwned
		}
		if err := validateSiteFloorEffects(state, row); err != nil {
			return nil, err
		}
		if op.Phase != "Verified" || row.Section != "sites" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.Before(creator.receipt.VerifiedAt) ||
			row.VerifiedAt == nil || row.VerifiedAt.IsZero() || row.VerifiedAt.Before(op.SubmittedAt) || row.VerifiedAt.After(time.Now()) || counts[op.ID] != 1 || row.Resource != siteAppliedResult(op, creator, parts[5]) {
			return nil, errConfigurationOwned
		}
		key := siteResourcePathDigest(op.Resource)
		if _, duplicate := out[key]; duplicate {
			return nil, errConfigurationOwned
		}
		canonical := row.DeepCopy()
		sort.Strings(canonical.EffectReceipts)
		out[key] = *canonical
	}
	return out, nil
}

func siteAppliedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if !siteAppliedSelected(op) || op.Phase != "Verified" {
		return false
	}
	rows, err := siteAppliedRecords(state)
	if err != nil {
		return false
	}
	row, ok := rows[siteResourcePathDigest(op.Resource)]
	return ok && reflect.DeepEqual(row.Operation, *op)
}

// The retained row also identifies the producer if an older schema drops a
// field from the current operation. A partial Verified operation cannot settle.
func siteOwnedSettlementRequired(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (bool, bool) {
	create, applied := siteCreateSelected(op), siteAppliedSelected(op)
	if state != nil && op != nil {
		for _, row := range state.ActionApplications {
			if row.Operation.ID != op.ID {
				continue
			}
			create = create || strings.HasPrefix(row.Resource, siteCreatedPrefix) || strings.HasPrefix(row.Resource, sitePrivateCreatedPrefix) || siteCreateSelected(&row.Operation)
			applied = applied || strings.HasPrefix(row.Resource, siteAppliedPrefix) || siteAppliedSelected(&row.Operation)
		}
	}
	return create, applied
}

func (e *siteConfigurationExecutor) privateUpdateResource(site desiredSite) (string, error) {
	creator, ok := siteCreatorForResource(e.creators, site.path)
	if !ok || e.privateResource == nil {
		return "", errConfigurationOwned
	}
	base, private := sitePrivateCreationParts(creator.receipt.Operation.Resource)
	current, valid := sitePrivateCreationParts(e.privateResource(site))
	if !private || !valid || !reflect.DeepEqual(base, current) {
		return "", errConfigurationSecret
	}
	prior := creator.receipt
	if row, exists := e.applied[sitePathDigest(site.path)]; exists {
		prior = row
	}
	return sitePrivateUpdatePrefix + strings.Join(base, ":") + ":" + siteReceiptDigest(prior), nil
}

func siteReplaceApplied(state *cvk.NetworkControllerConfigurationStatus, row cvk.NetworkControllerConfigurationActionApplication) error {
	key := siteResourcePathDigest(row.Operation.Resource)
	kept := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(state.ActionApplications)+1)
	for _, existing := range state.ActionApplications {
		if strings.HasPrefix(existing.Resource, siteAppliedPrefix) && siteResourcePathDigest(existing.Operation.Resource) == key {
			continue
		}
		kept = append(kept, existing)
	}
	state.ActionApplications = append(kept, row)
	_, err := siteAppliedRecords(state)
	return err
}

func siteApplyCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	creator, err := siteApplyCreator(state, op)
	if err != nil {
		return err
	}
	rows, err := siteAppliedRecords(state)
	if err != nil {
		return err
	}
	prior := creator.receipt
	if row, ok := rows[siteResourcePathDigest(op.Resource)]; ok {
		prior = row
	}
	parts := strings.Split(strings.TrimPrefix(op.Resource, sitePrivateUpdatePrefix), ":")
	floorParts, floorEffect := sitePrivateFloorUpdateParts(op.Resource)
	if floorEffect {
		parts = floorParts
	}
	if len(parts) != 5 && !floorEffect || len(parts) < 5 || parts[4] != siteReceiptDigest(prior) {
		return errConfigurationStale
	}
	for _, row := range state.ActionApplications {
		if row.Operation.ID == op.ID {
			return errConfigurationOwned
		}
	}
	future := op.DeepCopy()
	future.Phase = "Verified"
	future.TaskID = strings.Repeat("x", 128)
	now := metav1.Now()
	future.SubmittedAt = &now
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteAppliedResult(future, creator, strings.Repeat("a", 64)), Operation: *future, VerifiedAt: &now}
	if floorEffect {
		anchors, err := plannedAPIdentityRecords(state)
		if err != nil {
			return err
		}
		for key, anchor := range anchors {
			if anchor.receipt.Operation.ParentID == op.TargetID {
				row.EffectReceipts = append(row.EffectReceipts, plannedAPFloorEffectPrefix+strings.TrimPrefix(key, "planned-ap:")+":"+anchor.id+":"+anchor.keyID+":"+strings.Repeat("a", 64)+":"+strings.Repeat("b", 64))
			}
		}
		// Exact-size metadata placeholders validate scope/count/bytes only.
		// Expected native effects are independently checked around dispatch.
		root, err := plannedAPFloorEffectRoot(row.EffectReceipts)
		if err != nil {
			return err
		}
		row.Operation.Resource = row.Operation.Resource[:len(row.Operation.Resource)-64] + root
		row.Resource = siteAppliedResult(&row.Operation, creator, strings.Repeat("a", 64))
	}
	working := state.DeepCopy()
	if err := siteReplaceApplied(working, row); err != nil {
		return err
	}
	if floorEffect {
		// A floor write must not consume the final slot/bytes and then strand
		// an AP correction. Reserve worst-case latest envelopes for all retained
		// members, replacing existing rows in the size-only projection as well.
		applied, err := plannedAPAppliedRecords(state)
		if err != nil {
			return err
		}
		anchors, err := plannedAPIdentityRecords(state)
		if err != nil {
			return err
		}
		for key, anchor := range anchors {
			if anchor.receipt.Operation.ParentID != op.TargetID {
				continue
			}
			if _, exists := applied[key]; exists {
				kept := working.ActionApplications[:0]
				for _, existing := range working.ActionApplications {
					if strings.HasPrefix(existing.Resource, plannedAPAppliedPrefix) && plannedAPResourceKey(existing.Operation.Resource) == key {
						continue
					}
					kept = append(kept, existing)
				}
				working.ActionApplications = kept
			}
			base, ok := plannedAPPrivateParts(anchor.receipt.Operation.Resource)
			if !ok || len(base) != 3 {
				return errConfigurationOwned
			}
			correction := future.DeepCopy()
			correction.ID = strings.Repeat("0", 32)
			correction.ParentID, correction.TargetID = op.TargetID, anchor.id
			correction.Resource = plannedAPFloorUpdatePrefix + strings.Join(base, ":") + ":" + strings.Repeat("b", 64) + ":" + strings.Repeat("a", 64) + ":" + future.ID
			// This size-only envelope is never persisted or decoded as proof.
			working.ActionApplications = append(working.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPAppliedResult(correction, anchor), Operation: *correction, VerifiedAt: &now})
		}
	}
	raw, err := json.Marshal(working.ActionApplications)
	if err != nil || len(working.ActionApplications) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}

func (e *siteConfigurationExecutor) verifyApplied(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) ([]byte, error) {
	if !siteAppliedSelected(op) || !siteSingularOperation(op) || op.SubmittedAt == nil || !validAPIID(op.TaskID) || e.comparison == nil || e.comparison.check == nil {
		return nil, errConfigurationTransition
	}
	if err := e.comparison.check(ctx); err != nil {
		return nil, err
	}
	done, err := e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	raw, err := e.client.get(ctx, taskPath+op.TaskID, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	done, err = decodeConfigurationTaskComplete(raw, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	var result struct {
		Response struct {
			Data string `json:"data"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Response.Data != op.TargetID {
		return nil, errConfigurationOwned
	}
	verified, err := e.verify(ctx, op)
	if err != nil {
		return nil, err
	}
	if !verified {
		return nil, errConfigurationBusy
	}
	desired, ok := siteDesiredOperation(e.desired, op.Resource)
	if !ok {
		return nil, errConfigurationTransition
	}
	actual, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.checkCreatorIdentities(actual); err != nil {
		return nil, err
	}
	found := false
	for _, site := range actual {
		if site.NameHierarchy == desired.path {
			if site.ID != op.TargetID || site.ParentID != op.ParentID || site.Type != desired.kind {
				return nil, errConfigurationOwned
			}
			found = true
		}
	}
	if !found {
		return nil, errConfigurationBusy
	}
	drift, err := planSites([]desiredSite{*desired}, actual)
	if err != nil {
		return nil, err
	}
	if len(drift) != 0 {
		return nil, errConfigurationBusy
	}
	view, err := e.client.siteNativeFootprint(ctx, actual, desired.path, false)
	if err != nil {
		return nil, err
	}
	if _, err := siteNativeMAC(e.comparison, op.Resource, view); err != nil {
		return nil, err
	}
	if err := e.comparison.check(ctx); err != nil {
		return nil, err
	}
	return view, nil
}

func (a *adapter) recordSiteApplied(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *siteConfigurationExecutor, op *cvk.NetworkControllerConfigurationOperation) error {
	if !siteAppliedSelected(op) || op.Phase != "Verifying" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
		return err
	}
	if err := e.creatorRecordsMatch(current.Status.Configuration); err != nil {
		return err
	}
	_, floorEffect := sitePrivateFloorUpdateParts(op.Resource)
	if floorEffect {
		if e.verifyEffects == nil || e.effectRecordsMatch == nil {
			return errConfigurationTransition
		}
		if err := e.effectRecordsMatch(current.Status.Configuration); err != nil {
			return err
		}
	}
	if err := siteApplyCapacity(current.Status.Configuration, op); err != nil {
		return err
	}
	creator, err := siteApplyCreator(current.Status.Configuration, op)
	if err != nil {
		return err
	}
	view, err := e.verifyApplied(ctx, op)
	if err != nil {
		return err
	}
	var effects []string
	if floorEffect {
		effects, err = e.verifyEffects(ctx, op)
		if err != nil {
			return err
		}
	}
	fresh, err := e.verifyApplied(ctx, op)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(view, fresh) {
		return errConfigurationStale
	}
	if floorEffect {
		again, err := e.verifyEffects(ctx, op)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(effects, again) {
			return errConfigurationStale
		}
		latest, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if latest.UID != nc.UID || latest.Generation != nc.Generation || latest.Spec.Paused || !latest.DeletionTimestamp.IsZero() || latest.Status.Configuration == nil || !reflect.DeepEqual(latest.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		if err := e.creatorRecordsMatch(latest.Status.Configuration); err != nil {
			return err
		}
		if err := e.effectRecordsMatch(latest.Status.Configuration); err != nil {
			return err
		}
		current = latest
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	mac, err := siteNativeMAC(e.comparison, op.Resource, view)
	if err != nil {
		return err
	}
	current.Status.Configuration.Operation.Phase = "Verified"
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if op.SubmittedAt.After(now.Time) {
		now = *op.SubmittedAt.DeepCopy()
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteAppliedResult(op, creator, mac), Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &now, EffectReceipts: effects}
	if err := siteReplaceApplied(current.Status.Configuration, row); err != nil {
		return err
	}
	return a.persistConfigurationActionReceipt(ctx, current)
}
