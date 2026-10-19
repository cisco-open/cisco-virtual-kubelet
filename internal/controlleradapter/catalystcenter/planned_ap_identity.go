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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const plannedAPObservePrefix = "planned-ap-observe:v1:"
const plannedAPIdentityPrefix = "planned-ap-identity:v1:"

// This receipt records the identity observed after an acknowledged create task.
// The native task does not return a created UUID. It is deliberately not proof
// of exclusive creation, a complete native-state baseline or delete authority.
type plannedAPIdentityRecord struct {
	id         string
	keyID, mac string
	receipt    cvk.NetworkControllerConfigurationActionApplication
}

func plannedAPResourceKey(resource string) string {
	if parts, ok := plannedAPPrivateParts(resource); ok {
		return "planned-ap:" + parts[0]
	}
	for _, prefix := range []string{"planned-ap:", plannedAPObservePrefix} {
		if digest, ok := strings.CutPrefix(resource, prefix); ok && validTemplateComparisonDigest(digest) {
			return "planned-ap:" + digest
		}
	}
	return ""
}
func plannedAPResourceMatches(resource string, want plannedAPIntent) bool {
	return plannedAPResourceKey(resource) == plannedAPResource(want)
}
func plannedAPObservationSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	return op != nil && (strings.HasPrefix(op.Resource, plannedAPObservePrefix) || strings.HasPrefix(op.Resource, plannedAPPrivateObservePrefix)) && op.Action == "Create" && siteDesignOperation(op)
}
func plannedAPIdentityResource(op *cvk.NetworkControllerConfigurationOperation, id string, baseline ...string) string {
	if !plannedAPObservationSelected(op) || !canonicalTemplateUUID(id) {
		return ""
	}
	mode := "i"
	if op.SourceUID != "" {
		mode = "c"
	}
	if parts, ok := plannedAPPrivateParts(op.Resource); ok {
		if parts[1] != mode || len(baseline) != 1 || !validTemplateComparisonDigest(baseline[0]) {
			return ""
		}
		return plannedAPPrivateIdentityPrefix + parts[0] + ":" + mode + ":" + id + ":" + parts[2] + ":" + baseline[0]
	}
	if len(baseline) != 0 {
		return ""
	}
	return plannedAPIdentityPrefix + strings.TrimPrefix(op.Resource, plannedAPObservePrefix) + ":" + mode + ":" + id
}
func plannedAPIdentityRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]plannedAPIdentityRecord, error) {
	out := map[string]plannedAPIdentityRecord{}
	if state == nil {
		return out, nil
	}
	counts, ids := map[string]int{}, map[string]bool{}
	for _, r := range state.ActionApplications {
		counts[r.Operation.ID]++
	}
	for _, r := range state.ActionApplications {
		op := &r.Operation
		if !strings.HasPrefix(r.Resource, plannedAPIdentityPrefix) && !strings.HasPrefix(op.Resource, plannedAPObservePrefix) && !strings.HasPrefix(r.Resource, plannedAPPrivateIdentityPrefix) && !strings.HasPrefix(op.Resource, plannedAPPrivateObservePrefix) {
			continue
		}
		if len(r.EffectReceipts) != 0 {
			return nil, errConfigurationOwned
		}
		parts := strings.Split(strings.TrimPrefix(r.Resource, plannedAPIdentityPrefix), ":")
		private, keyID, mac := strings.HasPrefix(r.Resource, plannedAPPrivateIdentityPrefix), "", ""
		if private {
			parts = strings.Split(strings.TrimPrefix(r.Resource, plannedAPPrivateIdentityPrefix), ":")
			if len(parts) == 5 {
				keyID, mac = parts[3], parts[4]
			}
		}
		result := ""
		if len(parts) >= 3 {
			if private {
				result = plannedAPIdentityResource(op, parts[2], mac)
			} else {
				result = plannedAPIdentityResource(op, parts[2])
			}
		}
		if (!private && len(parts) != 3 || private && (len(parts) != 5 || !validTemplateComparisonDigest(keyID) || !validTemplateComparisonDigest(mac))) || !validTemplateComparisonDigest(parts[0]) || !canonicalTemplateUUID(parts[2]) || !canonicalTemplateUUID(op.ParentID) || r.Section != "sites" || !plannedAPObservationSelected(op) || op.Phase != "Verified" || r.Resource != result || result == "" || !configurationOperationID.MatchString(op.ID) || counts[op.ID] != 1 || op.ConfigName == "" || op.ConfigUID == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || (op.SourceUID == "") != (op.SourceVersion == "") {
			return nil, errConfigurationOwned
		}
		key := plannedAPResourceKey(op.Resource)
		if _, exists := out[key]; exists || ids[parts[2]] {
			return nil, errConfigurationOwned
		}
		ids[parts[2]] = true
		r.Operation = *r.Operation.DeepCopy()
		r.VerifiedAt = r.VerifiedAt.DeepCopy()
		out[key] = plannedAPIdentityRecord{id: parts[2], receipt: r, keyID: keyID, mac: mac}
	}
	return out, nil
}
func plannedAPIdentityReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	records, err := plannedAPIdentityRecords(state)
	if err != nil || op == nil || op.Phase != "Verified" {
		return false
	}
	record, exists := records[plannedAPResourceKey(op.Resource)]
	return exists && reflect.DeepEqual(record.receipt.Operation, *op)
}
func plannedAPIdentitySettlementRequired(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	if strings.HasPrefix(op.Resource, plannedAPObservePrefix) || strings.HasPrefix(op.Resource, plannedAPPrivateObservePrefix) {
		return true
	}
	if state != nil {
		for _, r := range state.ActionApplications {
			if r.Operation.ID == op.ID && (strings.HasPrefix(r.Resource, plannedAPIdentityPrefix) || strings.HasPrefix(r.Operation.Resource, plannedAPObservePrefix) || strings.HasPrefix(r.Resource, plannedAPPrivateIdentityPrefix) || strings.HasPrefix(r.Operation.Resource, plannedAPPrivateObservePrefix)) {
				return true
			}
		}
	}
	return false
}
func plannedAPIdentityCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || !plannedAPObservationSelected(op) || !canonicalTemplateUUID(op.ParentID) {
		return errConfigurationTransition
	}
	records, err := plannedAPIdentityRecords(state)
	if err != nil {
		return err
	}
	if _, exists := records[plannedAPResourceKey(op.Resource)]; exists {
		return errConfigurationOwned
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID {
			return errConfigurationOwned
		}
	}
	future, at := op.DeepCopy(), metav1.Now()
	future.Phase, future.TaskID, future.SubmittedAt = "Verified", strings.Repeat("x", 128), &at
	receipts := append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...)
	result := plannedAPIdentityResource(future, "11111111-1111-4111-8111-111111111111")
	if _, private := plannedAPPrivateParts(future.Resource); private {
		result = plannedAPIdentityResource(future, "11111111-1111-4111-8111-111111111111", strings.Repeat("a", 64))
	}
	receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: result, Operation: *future, VerifiedAt: &at})
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}
func (e *plannedAPExecutor) identityRecordsMatch(state *cvk.NetworkControllerConfigurationStatus) error {
	records, err := plannedAPIdentityRecords(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(records, e.identities) {
		return errConfigurationStale
	}
	applied, err := plannedAPAppliedRecords(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(applied, e.applied) && !(len(applied) == 0 && len(e.applied) == 0) {
		return errConfigurationStale
	}
	floors, err := plannedAPFloorAuthorities(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(floors, e.floorEffects) && !(len(floors) == 0 && len(e.floorEffects) == 0) {
		return errConfigurationStale
	}
	return nil
}
func (e *plannedAPExecutor) checkFloorIdentities(actual []Site) error {
	for _, want := range e.desired {
		record, exists := e.identities[plannedAPResource(want)]
		if !exists {
			continue
		}
		if record.receipt.Operation.ConfigUID != e.identityOwner {
			return errConfigurationOwned
		}
		found := false
		for _, site := range actual {
			if site.NameHierarchy == want.floor {
				found = site.Type == "floor" && site.ID == record.receipt.Operation.ParentID
			}
		}
		if !found {
			return errConfigurationOwned
		}
	}
	return nil
}
func (e *plannedAPExecutor) verifyIdentityObservation(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, []byte, error) {
	if (!plannedAPObservationSelected(op) && !plannedAPAppliedSelected(op)) || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
		return "", nil, errConfigurationTransition
	}
	done, err := e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return "", nil, err
	}
	if !done {
		return "", nil, errConfigurationBusy
	}
	if parts, private := plannedAPPrivateParts(op.Resource); private {
		if e.comparison == nil || e.comparison.check == nil || parts[2] != e.comparison.keyID || parts[1] != e.sourceMode {
			return "", nil, errConfigurationSecret
		}
		if err := e.comparison.check(ctx); err != nil {
			return "", nil, err
		}
	}
	observations, err := e.observeMode(ctx, true)
	if err != nil {
		return "", nil, err
	}
	for _, o := range observations {
		if !plannedAPResourceMatches(op.Resource, o.want) {
			continue
		}
		if o.floorID != op.ParentID || !canonicalTemplateUUID(o.actual.ID) || plannedAPAppliedSelected(op) && o.actual.ID != op.TargetID {
			return "", nil, errConfigurationOwned
		}
		body, changed, err := plannedAPUpdatePayload(o.want, o.actual)
		if err != nil {
			return "", nil, err
		}
		if changed {
			return "", nil, errConfigurationBusy
		}
		geometry, err := plannedAPObservedGeometry(o.floor)
		if err != nil {
			return "", nil, err
		}
		if err := validatePlannedAPPosition(body, geometry); err != nil {
			return "", nil, err
		}
		view, err := plannedAPDispatchView([]plannedAPState{o})
		if parts, private := plannedAPPrivateParts(op.Resource); private {
			view, err = plannedAPBaselineView(o)
			if err != nil {
				return "", nil, err
			}
			mac, err := plannedAPNativeMAC(e.comparison, op.Resource, view)
			if err != nil {
				return "", nil, err
			}
			if plannedAPAppliedSelected(op) && !hmac.Equal([]byte(mac), []byte(parts[4])) {
				return "", nil, errConfigurationStale
			}
			if err := e.comparison.check(ctx); err != nil {
				return "", nil, err
			}
		}
		return o.actual.ID, view, err
	}
	return "", nil, errConfigurationTransition
}
func (a *adapter) recordPlannedAPIdentity(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *plannedAPExecutor, op *cvk.NetworkControllerConfigurationOperation) error {
	if (!plannedAPObservationSelected(op) && !plannedAPAppliedSelected(op)) || op.Phase != "Verifying" || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
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
	if err := e.identityRecordsMatch(current.Status.Configuration); err != nil {
		return err
	}
	capacity := plannedAPIdentityCapacity
	if plannedAPAppliedSelected(op) {
		capacity = plannedAPApplyCapacity
	}
	if err := capacity(current.Status.Configuration, op); err != nil {
		return err
	}
	id, view, err := e.verifyIdentityObservation(ctx, op)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	again, fresh, err := e.verifyIdentityObservation(ctx, op)
	if err != nil {
		return err
	}
	if id != again || !reflect.DeepEqual(view, fresh) {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	current.Status.Configuration.Operation.Phase = "Verified"
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if op.SubmittedAt.After(at.Time) {
		at = *op.SubmittedAt.DeepCopy()
	}
	result := plannedAPIdentityResource(op, id)
	if _, private := plannedAPPrivateParts(op.Resource); private {
		mac, err := plannedAPNativeMAC(e.comparison, op.Resource, view)
		if err != nil {
			return err
		}
		result = plannedAPIdentityResource(op, id, mac)
	}
	if plannedAPAppliedSelected(op) {
		anchor, err := plannedAPApplyAnchor(current.Status.Configuration, op)
		if err != nil {
			return err
		}
		row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPAppliedResult(op, anchor), Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &at}
		if err := plannedAPReplaceApplied(current.Status.Configuration, row); err != nil {
			return err
		}
	} else {
		if result == "" {
			return errConfigurationTransition
		}
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: result, Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &at})
	}
	return a.persistConfigurationActionReceipt(ctx, current)
}
