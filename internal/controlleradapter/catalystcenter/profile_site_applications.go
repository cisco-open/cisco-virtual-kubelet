// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const profileSiteAddFamily = "profile-site-add:"
const profileSiteAddPrefix = profileSiteAddFamily + "v1:"
const profileSiteAppliedPrefix = "profile-site-applied:v1:"
const profileSiteRemoveFamily = "profile-site-remove:"
const profileSiteRemovePrefix = profileSiteRemoveFamily + "v1:"
const profileSiteRemovedPrefix = "profile-site-removed:v1:"

var errProfileSiteApplications = errors.New("profile/site application evidence is unavailable or changed")

func profileSiteDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func profileSiteDigestValid(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 64 && value == strings.ToLower(value) && err == nil
}
func profileSiteAddParts(local string) (key, predecessor string, ok bool) {
	return profileSiteParts(local, profileSiteAddPrefix)
}
func profileSiteParts(local, prefix string) (key, predecessor string, ok bool) {
	if !strings.HasPrefix(local, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(local, prefix), ":")
	if len(parts) != 2 || !profileSiteDigestValid(parts[0]) || !profileSiteDigestValid(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func profileSiteApplicationParts(resource string) (namespace, key, predecessor string, ok bool) {
	for _, prefix := range []string{"sites/", "wireless-sites/"} {
		if local, found := strings.CutPrefix(resource, prefix); found {
			key, predecessor, ok = profileSiteAddParts(local)
			if !ok {
				key, predecessor, ok = profileSiteParts(local, profileSiteRemovePrefix)
			}
			return prefix, key, predecessor, ok
		}
	}
	return "", "", "", false
}
func profileSiteApplicationSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	for _, prefix := range []string{"sites/", "wireless-sites/"} {
		if local, found := strings.CutPrefix(op.Resource, prefix); found && !strings.Contains(local, "/") && (strings.HasPrefix(local, profileSiteAddFamily) || strings.HasPrefix(local, profileSiteRemoveFamily)) {
			return true
		}
	}
	return false
}
func profileSiteResourceMatches(local, logical string) bool {
	if key, _, ok := profileSiteAddParts(local); ok {
		return key == profileSiteDigest(logical)
	}
	return local == logical
}
func profileSiteApplicationEnvelope(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	if _, _, _, ok := profileSiteApplicationParts(op.Resource); !ok {
		return false
	}
	copy := op.DeepCopy()
	copy.Phase = "Unknown"
	return networkProfileMetadataEnvelope(copy)
}
func profileSiteApplicationBase(namespace, key string) string {
	return profileSiteAppliedPrefix + strings.TrimSuffix(namespace, "/") + ":" + key
}
func profileSiteOperationBase(op *cvk.NetworkControllerConfigurationOperation) string {
	ns, key, _, _ := profileSiteApplicationParts(op.Resource)
	if op.Action == "Delete" {
		return profileSiteRemovedPrefix + strings.TrimSuffix(ns, "/") + ":" + key
	}
	return profileSiteApplicationBase(ns, key)
}
func profileSiteRowSeal(row cvk.NetworkControllerConfigurationActionApplication) string {
	row.Resource = profileSiteOperationBase(&row.Operation)
	raw, _ := json.Marshal(row)
	return profileSiteDigest(string(raw))
}
func profileSiteBookkeepingDigest(state *cvk.NetworkControllerConfigurationStatus, normalizeID string) string {
	return configurationBookkeepingDigest(state, func(row *cvk.NetworkControllerConfigurationActionApplication) {
		if normalizeID != "" && row.Operation.ID == normalizeID {
			if _, _, _, ok := profileSiteApplicationParts(row.Operation.Resource); ok {
				row.Resource = profileSiteOperationBase(&row.Operation) + ":" + profileSiteRowSeal(*row)
			}
		}
	})
}
func profileSiteApplicationRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	rows := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return rows, nil
	}
	nonces := map[string]int{}
	for _, row := range state.ActionApplications {
		nonces[row.Operation.ID]++
	}
	pairs := map[string]bool{}
	for _, row := range state.ActionApplications {
		if !strings.HasPrefix(row.Resource, "profile-site-applied:") && !strings.HasPrefix(row.Resource, "profile-site-removed:") && !profileSiteApplicationSelected(&row.Operation) {
			continue
		}
		op := &row.Operation
		ns, key, _, ok := profileSiteApplicationParts(op.Resource)
		base := profileSiteOperationBase(op) + ":"
		seals := strings.Split(strings.TrimPrefix(row.Resource, base), ":")
		if !ok || !profileSiteApplicationEnvelope(op) || row.Section != "network_profiles" || op.Phase != "Verified" || !strings.HasPrefix(row.Resource, base) || len(seals) != 2 || !profileSiteDigestValid(seals[0]) || !profileSiteDigestValid(seals[1]) || seals[0] != profileSiteRowSeal(row) || nonces[op.ID] != 1 || op.SubmittedAt.After(time.Now()) || row.VerifiedAt == nil || row.VerifiedAt.IsZero() || row.VerifiedAt.Before(op.SubmittedAt) || row.VerifiedAt.After(time.Now()) || len(row.EffectReceipts) != 0 {
			return nil, errProfileSiteApplications
		}
		owners := 0
		for _, owner := range state.Owners {
			if owner.Section == "network_profiles" {
				if owner.ConfigName != op.ConfigName || owner.ConfigUID != op.ConfigUID {
					return nil, errProfileSiteApplications
				}
				owners++
			}
		}
		identity := ns + key
		pair := op.ParentID + "\x00" + op.TargetID
		if owners != 1 || pairs[pair] {
			return nil, errProfileSiteApplications
		}
		if _, exists := rows[identity]; exists {
			return nil, errProfileSiteApplications
		}
		pairs[pair] = true
		rows[identity] = *row.DeepCopy()
	}
	return rows, nil
}
func profileSiteApplicationPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Phase != "Verified" {
		return false
	}
	rows, err := profileSiteApplicationRecords(state)
	if err != nil {
		return false
	}
	ns, key, _, ok := profileSiteApplicationParts(op.Resource)
	row, exists := rows[ns+key]
	return ok && exists && reflect.DeepEqual(row.Operation, *op) && row.Resource == profileSiteOperationBase(op)+":"+profileSiteRowSeal(row)+":"+profileSiteBookkeepingDigest(state, op.ID)
}

func (a *adapter) bindProfileSiteApplications(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan == nil || plan.section != "network_profiles" {
		return nil
	}
	e, ok := plan.executor.(*networkProfileConfiguration)
	if !ok {
		return nil
	}
	retained, err := profileSiteApplicationRecords(nc.Status.Configuration)
	if err != nil {
		return err
	}
	if nc.Status.Configuration != nil && profileSiteApplicationSelected(nc.Status.Configuration.Operation) {
		op := nc.Status.Configuration.Operation
		_, _, _, valid := profileSiteApplicationParts(op.Resource)
		if !valid || !networkProfileMetadataKindSupported(op) || op.Phase == "Verified" && !profileSiteApplicationPresent(nc.Status.Configuration, op) {
			return errProfileSiteApplications
		}
	}
	children := map[string]*profileSiteExecutor{}
	for _, part := range e.parts {
		if child, ok := part.executor.(*profileSiteExecutor); ok {
			children[part.name+"/"] = child
		}
	}
	guard := func(ctx context.Context, namespace string, observed []profileSiteObservation) (string, error) {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return "", err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return "", err
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return "", err
		}
		rows, err := profileSiteApplicationRecords(current.Status.Configuration)
		if err != nil || !reflect.DeepEqual(rows, retained) {
			return "", errProfileSiteApplications
		}
		missing := 0
		for ns, child := range children {
			observations := observed
			if ns != namespace {
				observations, err = child.observe(ctx)
				if err != nil {
					return "", err
				}
			}
			for _, row := range observations {
				identity := ns + profileSiteDigest(row.resource)
				if old, exists := rows[identity]; exists {
					if old.Operation.Action == "Delete" && row.present || row.profileID != "" && old.Operation.ParentID != row.profileID || old.Operation.TargetID != row.siteID || old.Operation.ConfigUID != string(config.UID) || old.Operation.ConfigName != config.Name {
						return "", errProfileSiteApplications
					}
				} else if !row.present {
					missing++
				}
				for oldKey, old := range rows {
					if oldKey != identity && row.profileID != "" && old.Operation.ParentID == row.profileID && old.Operation.TargetID == row.siteID {
						return "", errProfileSiteApplications
					}
				}
			}
		}
		if len(current.Status.Configuration.ActionApplications)+missing > 256 {
			return "", errConfigurationBusy
		}
		return profileSiteBookkeepingDigest(current.Status.Configuration, ""), nil
	}
	for ns, child := range children {
		child.applicationPlan = func(ctx context.Context, rows []profileSiteObservation) (string, error) { return guard(ctx, ns, rows) }
		child.applicationProof = func(ctx context.Context, predecessor string) error {
			if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
				return err
			}
			current, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
				return err
			}
			if _, err := profileSiteApplicationRecords(current.Status.Configuration); err != nil {
				return err
			}
			if profileSiteBookkeepingDigest(current.Status.Configuration, "") != predecessor {
				return errProfileSiteApplications
			}
			return nil
		}
	}
	previousRecord, previousSelect := plan.recordCompletion, plan.receiptOperation
	selected := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && profileSiteApplicationSelected(op)
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return selected(op) || previousRecord != nil && (previousSelect == nil || previousSelect(op))
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if !selected(op) {
			if previousRecord != nil {
				return previousRecord(ctx, op)
			}
			return errConfigurationTransition
		}
		return a.recordProfileSiteApplication(ctx, config, source, e.observeProfileMetadata, op, retained)
	}
	return nil
}

func (a *adapter) recordProfileSiteApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, observe func(context.Context, *cvk.NetworkControllerConfigurationOperation) (any, bool, error), op *cvk.NetworkControllerConfigurationOperation, expected map[string]cvk.NetworkControllerConfigurationActionApplication) error {
	if op == nil || op.Phase != "Verifying" || !profileSiteApplicationEnvelope(op) {
		return errConfigurationTransition
	}
	ns, key, predecessor, _ := profileSiteApplicationParts(op.Resource)
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
		return errConfigurationStale
	}
	rows, err := profileSiteApplicationRecords(current.Status.Configuration)
	if err != nil || !reflect.DeepEqual(rows, expected) || profileSiteBookkeepingDigest(current.Status.Configuration, "") != predecessor {
		return errProfileSiteApplications
	}
	if old, exists := rows[ns+key]; exists {
		if op.Action == "Delete" && old.Operation.Action != "Update" {
			return errProfileSiteApplications
		}
		if old.Operation.ParentID != op.ParentID || old.Operation.TargetID != op.TargetID || old.Operation.ConfigUID != op.ConfigUID || old.Operation.ConfigName != op.ConfigName || old.Operation.ConfigGeneration > op.ConfigGeneration || old.Operation.ControllerGeneration > op.ControllerGeneration || op.SubmittedAt.Before(old.VerifiedAt) {
			return errProfileSiteApplications
		}
	} else if op.Action == "Delete" || len(current.Status.Configuration.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	observed := op.DeepCopy()
	observed.Phase = "Unknown"
	done, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "network_profiles", func(ctx context.Context) (bool, error) {
		before, beforeDone, err := observe(ctx, observed)
		if err != nil {
			return false, err
		}
		done, err := a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
		if err != nil || !done {
			return false, err
		}
		done, err = a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
		if err != nil || !done {
			return false, err
		}
		after, afterDone, err := observe(ctx, observed)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(before, after) {
			return false, errConfigurationStale
		}
		return beforeDone && afterDone, nil
	})
	if err != nil {
		return err
	}
	if !done {
		return errConfigurationBusy
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != current.UID || fresh.ResourceVersion != current.ResourceVersion || !reflect.DeepEqual(fresh.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if at.Before(op.SubmittedAt) {
		return errConfigurationBusy
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "network_profiles", Resource: profileSiteOperationBase(verified), Operation: *verified, VerifiedAt: &at}
	row.Resource += ":" + profileSiteRowSeal(row)
	index := -1
	for i, old := range current.Status.Configuration.ActionApplications {
		ons, okey, _, ok := profileSiteApplicationParts(old.Operation.Resource)
		if ok && ons == ns && okey == key {
			index = i
		}
	}
	if index < 0 {
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, row)
		index = len(current.Status.Configuration.ActionApplications) - 1
	} else {
		current.Status.Configuration.ActionApplications[index] = row
	}
	current.Status.Configuration.Operation = verified
	current.Status.Configuration.ActionApplications[index].Resource += ":" + profileSiteBookkeepingDigest(current.Status.Configuration, op.ID)
	if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if !profileSiteApplicationPresent(stored.Status.Configuration, verified) {
		return errProfileSiteApplications
	}
	return nil
}
