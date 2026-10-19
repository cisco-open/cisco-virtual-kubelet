// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const networkSettingUpdateFamily = "network-setting-update:"
const networkSettingUpdatePrefix = networkSettingUpdateFamily + "v1:"
const networkSettingAppliedPrefix = "network-setting-applied:v1:"

var errNetworkApplicationHistory = errors.New("network setting application history changed or is invalid")

func networkSettingLogicalKey(logical string) string { return networkSettingDigest([]byte(logical)) }
func networkSettingDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func networkSettingDigestValid(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func networkSettingUpdateParts(resource string) (string, string, bool) {
	if parts, private := networkPrivateParts(resource); private {
		return parts[0], parts[1], true
	}
	if !strings.HasPrefix(resource, networkSettingUpdatePrefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(resource, networkSettingUpdatePrefix), ":")
	if len(parts) != 2 || !networkSettingDigestValid(parts[0]) || !networkSettingDigestValid(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func networkSettingResourceKey(resource string) (string, bool) {
	if key, _, ok := networkSettingUpdateParts(resource); ok {
		return key, true
	}
	index := strings.LastIndex(resource, "#")
	if index < 0 || !validSitePath(resource[:index]) {
		return "", false
	}
	switch resource[index+1:] {
	case "ntp", "dhcp", "dns", "timezone", "banner", "telemetry":
		return networkSettingLogicalKey(resource), true
	}
	return "", false
}
func networkSettingResourceMatches(resource, logical string) bool {
	key, ok := networkSettingResourceKey(resource)
	return ok && key == networkSettingLogicalKey(logical)
}
func networkSettingApplicationSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	return op != nil && strings.HasPrefix(op.Resource, networkSettingUpdateFamily)
}
func networkSettingApplicationEnvelope(op *cvk.NetworkControllerConfigurationOperation) bool {
	if !networkSettingApplicationSelected(op) {
		return false
	}
	if _, _, ok := networkSettingUpdateParts(op.Resource); !ok {
		return false
	}
	copy := op.DeepCopy()
	copy.Phase = "Unknown"
	return networkSettingKnownTaskSupported(copy)
}

// Digest public bookkeeping only, never native setting values. The active row's
// result identity is normalized to avoid a self-reference. Old seals remain
// historical; only the active Verified operation requires its commit cohort.
func networkSettingBookkeepingDigest(state *cvk.NetworkControllerConfigurationStatus, normalizeResultFor string) string {
	return configurationBookkeepingDigest(state, func(row *cvk.NetworkControllerConfigurationActionApplication) {
		if normalizeResultFor != "" && row.Operation.ID == normalizeResultFor {
			if key, _, ok := networkSettingUpdateParts(row.Operation.Resource); ok {
				row.Resource = networkAppliedBase(row.Operation.Resource, key)
			}
		}
	})
}

// The adapter-specific result identity carries the public commit seal. The
// shared EffectReceipts field remains reserved for related native effects.
func networkSettingApplicationResultValid(resource, key, operationResource string) bool {
	prefix := networkAppliedBase(operationResource, key) + ":"
	return strings.HasPrefix(resource, prefix) && networkSettingDigestValid(strings.TrimPrefix(resource, prefix))
}

func networkSettingApplicationRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	records := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return records, nil
	}
	nonces := map[string]int{}
	for _, row := range state.ActionApplications {
		nonces[row.Operation.ID]++
	}
	for _, row := range state.ActionApplications {
		if !strings.HasPrefix(row.Resource, "network-setting-applied:") && !networkSettingApplicationSelected(&row.Operation) {
			continue
		}
		op := &row.Operation
		key, _, valid := networkSettingUpdateParts(op.Resource)
		if !valid || !networkSettingApplicationEnvelope(op) || op.Phase != "Verified" || row.Section != "network_settings" || !networkSettingApplicationResultValid(row.Resource, key, op.Resource) || nonces[op.ID] != 1 || op.SubmittedAt.After(time.Now()) || row.VerifiedAt == nil || row.VerifiedAt.IsZero() || row.VerifiedAt.Before(op.SubmittedAt) || row.VerifiedAt.After(time.Now()) || len(row.EffectReceipts) != 0 {
			return nil, errNetworkApplicationHistory
		}
		if _, exists := records[key]; exists {
			return nil, errNetworkApplicationHistory
		}
		owners := 0
		for _, owner := range state.Owners {
			if owner.Section == "network_settings" {
				if owner.ConfigName != op.ConfigName || owner.ConfigUID != op.ConfigUID {
					return nil, errNetworkApplicationHistory
				}
				owners++
			}
		}
		if owners != 1 {
			return nil, errNetworkApplicationHistory
		}
		records[key] = *row.DeepCopy()
	}
	return records, nil
}
func networkSettingApplicationPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Phase != "Verified" {
		return false
	}
	records, err := networkSettingApplicationRecords(state)
	if err != nil {
		return false
	}
	key, _, ok := networkSettingUpdateParts(op.Resource)
	if !ok {
		return false
	}
	row, exists := records[key]
	return exists && reflect.DeepEqual(row.Operation, *op) && row.Resource == networkAppliedBase(op.Resource, key)+":"+networkSettingBookkeepingDigest(state, op.ID)
}

func (a *adapter) bindNetworkSettingApplications(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan == nil || plan.section != "network_settings" {
		return nil
	}
	settings := networkSettingsExecutor(plan.executor)
	if settings == nil {
		return nil
	}
	retained, err := networkSettingApplicationRecords(nc.Status.Configuration)
	if err != nil {
		return err
	}
	if err := checkNetworkPrivateKey(settings.comparison, retained); err != nil {
		return err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		active := nc.Status.Configuration.Operation
		if networkSettingApplicationSelected(active) && active.Phase == "Verified" && !networkSettingApplicationPresent(nc.Status.Configuration, active) {
			return errNetworkApplicationHistory
		}
	}
	// Even zero selected settings must validate retained custody and any
	// active Verified receipt above. Retain is not authority to erase history.
	if settings.count() == 0 {
		return nil
	}
	desired := freezeNetworkExecutor(settings)
	guard := func(ctx context.Context, observed networkSubmissionObservation) (string, error) {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return "", err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return "", err
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return "", errNetworkApplicationHistory
		}
		records, err := networkSettingApplicationRecords(current.Status.Configuration)
		if err != nil || !reflect.DeepEqual(records, retained) {
			return "", errNetworkApplicationHistory
		}
		missing := 0
		for _, binding := range desired.bindings {
			for _, want := range binding.settings {
				logical := networkSettingIdentity(binding.path, want.kind)
				key := networkSettingLogicalKey(logical)
				// A renamed path is not a fresh target: the original private
				// baseline still owns this native UUID until a qualified handoff.
				if want.kind == "telemetry" && desired.comparison != nil {
					for retainedKey, record := range records {
						if _, private := networkPrivateParts(record.Operation.Resource); private && retainedKey != key && record.Operation.TargetID == observed.sites[binding.path].id {
							return "", errNetworkNativeBaseline
						}
					}
				}
				if old, exists := records[key]; exists {
					if want.kind == "telemetry" && desired.comparison != nil {
						matched, err := networkMatchPrivate(ctx, desired.comparison, old.Operation.Resource, binding.path, observed)
						if err != nil {
							return "", err
						}
						if !matched {
							return "", errNetworkNativeBaseline
						}
					}
					if old.Operation.ConfigUID != string(config.UID) || old.Operation.ConfigName != config.Name || old.Operation.TargetID != observed.sites[binding.path].id {
						return "", errNetworkApplicationHistory
					}
				} else if len(networkSettingDrift(binding.path, want, observed.effective[logical])) > 0 {
					missing++
				}
			}
		}
		if len(current.Status.Configuration.ActionApplications)+missing > 256 {
			return "", errConfigurationBusy
		}
		return networkSettingBookkeepingDigest(current.Status.Configuration, ""), nil
	}
	settings.applicationPlan = guard
	settings.applicationProof = func(ctx context.Context, predecessor string) error {
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return errNetworkApplicationHistory
		}
		if _, err := networkSettingApplicationRecords(current.Status.Configuration); err != nil {
			return err
		}
		if networkSettingBookkeepingDigest(current.Status.Configuration, "") != predecessor {
			return errNetworkApplicationHistory
		}
		return nil
	}
	selections := networkSettingTaskSelections(plan.executor)
	previousRecord, previousSelection := plan.recordCompletion, plan.receiptOperation
	selected := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op == nil {
			return false
		}
		key, _, ok := networkSettingUpdateParts(op.Resource)
		return ok && networkSettingApplicationEnvelope(op) && selections[key] != nil
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && (selected(op) || previousRecord != nil && (previousSelection == nil || previousSelection(op)))
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if selected(op) {
			key, _, _ := networkSettingUpdateParts(op.Resource)
			return a.recordNetworkSettingApplication(ctx, config, source, selections[key], op, retained)
		}
		if previousRecord != nil {
			return previousRecord(ctx, op)
		}
		return errConfigurationTransition
	}
	return nil
}

func (a *adapter) recordNetworkSettingApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, settings *networkConfigurationExecutor, op *cvk.NetworkControllerConfigurationOperation, expected map[string]cvk.NetworkControllerConfigurationActionApplication) error {
	if op == nil || op.Phase != "Verifying" || !networkSettingApplicationEnvelope(op) {
		return errConfigurationTransition
	}
	key, predecessor, _ := networkSettingUpdateParts(op.Resource)
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
		return errConfigurationStale
	}
	records, err := networkSettingApplicationRecords(current.Status.Configuration)
	if err != nil || !reflect.DeepEqual(records, expected) || networkSettingBookkeepingDigest(current.Status.Configuration, "") != predecessor {
		return errNetworkApplicationHistory
	}
	if old, exists := records[key]; exists {
		if old.Operation.TargetID != op.TargetID || old.Operation.ConfigUID != op.ConfigUID || old.Operation.ConfigName != op.ConfigName || old.Operation.ConfigGeneration > op.ConfigGeneration || old.Operation.ControllerGeneration > op.ControllerGeneration || op.SubmittedAt.Before(old.VerifiedAt) {
			return errNetworkApplicationHistory
		}
	} else if len(current.Status.Configuration.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	done, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "network_settings", func(ctx context.Context) (bool, error) {
		observed := op.DeepCopy()
		observed.Phase = "Unknown"
		return settings.observeRetainedTask(ctx, observed, time.Now())
	})
	if err != nil {
		return err
	}
	if !done {
		return errConfigurationBusy
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
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
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "network_settings", Resource: networkAppliedBase(op.Resource, key), Operation: *verified, VerifiedAt: &at}
	index := -1
	for i, old := range current.Status.Configuration.ActionApplications {
		if oldKey, _, ok := networkSettingUpdateParts(old.Operation.Resource); ok && oldKey == key && old.Section == row.Section {
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
	current.Status.Configuration.ActionApplications[index].Resource += ":" + networkSettingBookkeepingDigest(current.Status.Configuration, op.ID)
	if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if !networkSettingApplicationPresent(stored.Status.Configuration, verified) {
		return errNetworkApplicationHistory
	}
	return nil
}
