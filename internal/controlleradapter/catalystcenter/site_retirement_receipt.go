// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const siteRetiredPrefix = "site-retired:v1:"

func siteRetiredResource(op *cvk.NetworkControllerConfigurationOperation) string {
	if !siteRetirementOperation(op) {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(op.Resource, siteRetirementPrefix), ":")
	return siteRetiredPrefix + parts[0] + ":" + parts[1]
}

// Catch both operation and retained-result schema loss. Sites Delete currently
// has no other public completion family, so an unrecognized Delete stays fenced.
func siteRetirementSettlementRequired(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	selected := strings.HasPrefix(op.Resource, "site-retire:") || op.Section == "sites" && op.Action == "Delete"
	if state != nil {
		for _, row := range state.ActionApplications {
			if row.Operation.ID == op.ID {
				selected = selected || strings.HasPrefix(row.Resource, "site-retired:") || strings.HasPrefix(row.Operation.Resource, "site-retire:") || row.Section == "sites" && row.Operation.Action == "Delete"
			}
		}
	}
	return selected
}

// Keep creator/latest producer rows intact. Retirement is a separate bounded
// receipt, not authority to erase lineage or hand the site to a new config UID.
func siteRetiredRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	if _, err := siteCreatedRecords(state); err != nil {
		return nil, err
	}
	rows, err := siteRetiredRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := siteHistoryTaskUnused(state, &row.Operation); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func siteRetiredRecordsCurrent(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out, nil
	}
	counts := map[string]int{}
	for _, row := range state.ActionApplications {
		counts[row.Operation.ID]++
	}
	now := time.Now()
	for index, row := range state.ActionApplications {
		if strings.HasPrefix(row.Resource, "site-history:") {
			continue
		}
		if !strings.HasPrefix(row.Resource, "site-retired:") && !strings.HasPrefix(row.Operation.Resource, "site-retire:") {
			continue
		}
		op := &row.Operation
		observed := op.DeepCopy()
		observed.Phase = "Verifying"
		if row.Section != "sites" || op.Phase != "Verified" || !siteRetirementKnownTask(observed) || row.Resource != siteRetiredResource(op) || row.Resource == "" || counts[op.ID] != 1 || len(row.EffectReceipts) != 0 || row.VerifiedAt == nil || row.VerifiedAt.IsZero() || row.VerifiedAt.After(now) || op.SubmittedAt.After(row.VerifiedAt.Time) {
			return nil, errConfigurationOwned
		}
		if _, duplicate := out[row.Resource]; duplicate {
			return nil, errConfigurationOwned
		}
		predecessor := state.DeepCopy()
		predecessor.ActionApplications = append(predecessor.ActionApplications[:index], predecessor.ActionApplications[index+1:]...)
		config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: op.ConfigName, UID: types.UID(op.ConfigUID), Generation: op.ConfigGeneration}, Spec: configapi.NetworkControllerConfigSpec{ManagedSections: []string{"sites"}}}
		if err := siteRetirementLineageCurrent(predecessor, config, op); err != nil {
			return nil, err
		}
		out[row.Resource] = *row.DeepCopy()
	}
	return out, nil
}

func siteRetiredReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Phase != "Verified" {
		return false
	}
	rows, err := siteRetiredRecords(state)
	if err != nil {
		return false
	}
	row, found := rows[siteRetiredResource(op)]
	return found && reflect.DeepEqual(row.Operation, *op)
}

func siteRetirementReceiptCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || !siteRetirementOperation(op) {
		return errConfigurationTransition
	}
	if _, err := siteRetiredRecords(state); err != nil {
		return err
	}
	resource := siteRetiredResource(op)
	for _, row := range state.ActionApplications {
		if row.Resource == resource || op.ID != "" && row.Operation.ID == op.ID {
			return errConfigurationOwned
		}
	}
	future := state.DeepCopy()
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	// Planning has no operation/task acknowledgement yet. Reserve their full
	// schema bounds before dispatch, rather than discovering exhaustion later.
	if verified.ID == "" {
		verified.ID = strings.Repeat("f", 32)
	}
	if verified.TaskID == "" {
		verified.TaskID = strings.Repeat("x", 128)
	}
	if verified.SubmittedAt == nil {
		verified.SubmittedAt = stamp.DeepCopy()
	}
	future.ActionApplications = append(future.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: resource, Operation: *verified, VerifiedAt: &stamp})
	raw, err := json.Marshal(future.ActionApplications)
	if err != nil || len(future.ActionApplications) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}

func (a *adapter) recordSiteRetired(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, id string) error {
	if config == nil || source == nil || !configurationOperationID.MatchString(id) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.ID != id {
		return errConfigurationStale
	}
	expected := nc.Status.Configuration.Operation.DeepCopy()
	if expected.ConfigName != config.Name || expected.ConfigUID != string(config.UID) || expected.ConfigGeneration != config.Generation || expected.ControllerGeneration != nc.Generation || expected.SourceUID != string(source.uid) || expected.SourceVersion != source.version {
		return errConfigurationStale
	}
	if expected.Phase == "Verified" {
		if !siteRetiredReceiptPresent(nc.Status.Configuration, expected) {
			return errConfigurationBusy
		}
		return nil // Lost-write-response recovery does not observe or submit again.
	}
	if err := siteRetirementReceiptCapacity(nc.Status.Configuration, expected); err != nil {
		return err
	}
	history, err := siteRetirementHistory(nc.Status.Configuration)
	if err != nil {
		return err
	}
	config = config.DeepCopy()
	source = freezeSiteRetirementSource(source)
	complete, err := a.observeSiteRetirementTask(ctx, config, source, expected, time.Now())
	if err != nil {
		return err
	}
	if !complete {
		return errConfigurationBusy
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, expected) {
		return errConfigurationStale
	}
	digest, err := siteRetirementHistory(current.Status.Configuration)
	if err != nil {
		return err
	}
	if digest != history {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if err := siteRetirementReceiptCapacity(current.Status.Configuration, expected); err != nil {
		return err
	}
	op := current.Status.Configuration.Operation
	op.Phase = "Verified"
	stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteRetiredResource(op), Operation: *op.DeepCopy(), VerifiedAt: &stamp})
	if _, err := siteRetiredRecords(current.Status.Configuration); err != nil {
		return err
	}
	if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if stored.Status.Configuration == nil || !siteRetiredReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
		return errConfigurationStale
	}
	// The common persistence helper checks operation+result. Also verify the
	// complete predecessor cohort: schema pruning must not retire creator custody.
	before := stored.Status.Configuration.DeepCopy()
	rows := before.ActionApplications[:0]
	for _, row := range before.ActionApplications {
		if row.Operation.ID != id {
			rows = append(rows, row)
		}
	}
	before.ActionApplications = rows
	digest, err = siteRetirementHistory(before)
	if err != nil {
		return err
	}
	if digest != history {
		return errConfigurationStale
	}
	return nil
}
