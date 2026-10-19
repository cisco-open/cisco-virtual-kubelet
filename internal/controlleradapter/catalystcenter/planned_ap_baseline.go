// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const plannedAPBaselineAnnotation = "cisco.vk/catalyst-center-private-planned-aps"
const plannedAPPrivateObservePrefix = "planned-ap-private-observe:v1:"
const plannedAPPrivateIdentityPrefix = "planned-ap-identity:v2:"
const plannedAPPrivateUpdatePrefix = "planned-ap-private-update:v1:"
const plannedAPAppliedPrefix = "planned-ap-applied:v1:"

// Base identity is logical resource digest, source mode and scoped key identity.
// Updates additionally bind both the exact prior receipt and intended result MAC.
func plannedAPPrivateParts(resource string) ([]string, bool) {
	prefix, n := plannedAPPrivateObservePrefix, 3
	if strings.HasPrefix(resource, plannedAPPrivateUpdatePrefix) {
		prefix, n = plannedAPPrivateUpdatePrefix, 5
	} else if strings.HasPrefix(resource, plannedAPFloorUpdatePrefix) {
		prefix, n = plannedAPFloorUpdatePrefix, 6
	}
	if !strings.HasPrefix(resource, prefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, prefix), ":")
	if len(parts) != n || !validTemplateComparisonDigest(parts[0]) || parts[1] != "i" && parts[1] != "c" || !validTemplateComparisonDigest(parts[2]) {
		return nil, false
	}
	if n >= 5 && (!validTemplateComparisonDigest(parts[3]) || !validTemplateComparisonDigest(parts[4])) {
		return nil, false
	}
	if n == 6 && !configurationOperationID.MatchString(parts[5]) {
		return nil, false
	}
	return parts, true
}
func plannedAPAppliedSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	return op != nil && op.Section == "sites" && (strings.HasPrefix(op.Resource, plannedAPPrivateUpdatePrefix) || strings.HasPrefix(op.Resource, plannedAPFloorUpdatePrefix))
}
func plannedAPPrivateRequired(state *cvk.NetworkControllerConfigurationStatus) bool {
	selected := func(resource string) bool {
		return strings.HasPrefix(resource, plannedAPPrivateObservePrefix) || strings.HasPrefix(resource, plannedAPPrivateUpdatePrefix) || strings.HasPrefix(resource, plannedAPFloorUpdatePrefix) || strings.HasPrefix(resource, sitePrivateFloorUpdatePrefix) || strings.HasPrefix(resource, plannedAPPrivateIdentityPrefix) || strings.HasPrefix(resource, plannedAPAppliedPrefix)
	}
	if state == nil {
		return false
	}
	if state.Operation != nil && selected(state.Operation.Resource) {
		return true
	}
	for _, r := range state.ActionApplications {
		if selected(r.Resource) || selected(r.Operation.Resource) {
			return true
		}
	}
	return false
}

// Only explicitly modeled position numbers and MAC representation normalize.
// Opaque values/numbers and array order remain exact; floor settings are owned
// separately, while floor identity and the entire native AP object are bound.
func plannedAPBaselineView(o plannedAPState) ([]byte, error) {
	if o.native == nil || o.actual.Position == nil || o.actual.MAC == nil {
		return nil, errInvalidResponse
	}
	raw, err := json.Marshal(o.native)
	if err != nil {
		return nil, errInvalidResponse
	}
	value, err := decodeExactNativeJSON(raw)
	if err != nil {
		return nil, err
	}
	native := value.(map[string]any)
	position, ok := native["position"].(map[string]any)
	if !ok {
		return nil, errInvalidResponse
	}
	position["x"], position["y"], position["z"] = o.actual.Position.X, o.actual.Position.Y, o.actual.Position.Z
	native["macAddress"] = *o.actual.MAC
	return json.Marshal([]any{o.floorID, o.actual.ID, native})
}
func plannedAPNativeMAC(comparison *templateInputComparison, resource string, view []byte) (string, error) {
	key := plannedAPResourceKey(resource)
	if key == "" {
		return "", errConfigurationTransition
	}
	if comparison == nil || len(comparison.key) != 32 || !validTemplateComparisonDigest(comparison.keyID) || comparison.check == nil {
		return "", errConfigurationSecret
	}
	if parts, private := plannedAPPrivateParts(resource); private && parts[2] != comparison.keyID {
		return "", errConfigurationSecret
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte("cvk-planned-ap-native-state-v1\x00" + comparison.keyID + "\x00" + key + "\x00"))
	mac.Write(view)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func (e *plannedAPExecutor) checkPrivateBaseline(ctx context.Context, o plannedAPState) error {
	record, exists := e.identities[plannedAPResource(o.want)]
	if e.comparison == nil {
		if exists && record.keyID != "" {
			return errConfigurationSecret
		}
		return nil
	}
	if e.comparison.check == nil {
		return errConfigurationSecret
	}
	if err := e.comparison.check(ctx); err != nil {
		return err
	}
	if !exists && o.actual.ID == "" {
		return nil
	}
	if !exists || record.keyID != e.comparison.keyID {
		return errConfigurationOwned
	}
	parts, ok := plannedAPPrivateParts(record.receipt.Operation.Resource)
	if !ok || parts[1] != e.sourceMode {
		return errConfigurationSecret
	}
	expected, _, _, err := plannedAPCurrentAuthority(plannedAPResource(o.want), record, e.applied, e.floorEffects)
	if err != nil {
		return err
	}
	view, err := plannedAPBaselineView(o)
	if err != nil {
		return err
	}
	actual, err := plannedAPNativeMAC(e.comparison, record.receipt.Operation.Resource, view)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(actual), []byte(expected)) {
		return errConfigurationStale
	}
	return e.comparison.check(ctx)
}
func (e *plannedAPExecutor) privateUpdateResource(o plannedAPState, body map[string]any) (string, error) {
	record, ok := e.identities[plannedAPResource(o.want)]
	if !ok || record.keyID == "" || e.comparison == nil || record.keyID != e.comparison.keyID {
		return "", errConfigurationOwned
	}
	parts, ok := plannedAPPrivateParts(record.receipt.Operation.Resource)
	if !ok || parts[1] != e.sourceMode {
		return "", errConfigurationSecret
	}
	// Compute the expected complete result before dispatch. Keep native fields
	// omitted from the bounded writer, including the qualified antenna extension.
	raw, _ := json.Marshal(o.native)
	value, err := decodeExactNativeJSON(raw)
	if err != nil {
		return "", err
	}
	after := plannedAPState{want: o.want, floorID: o.floorID, floor: o.floor, native: value.(map[string]any)}
	for _, key := range []string{"name", "type", "macAddress", "position"} {
		if v, ok := body[key]; ok {
			after.native[key] = v
		}
	}
	encoded, err := json.Marshal(after.native)
	if err != nil || json.Unmarshal(encoded, &after.actual) != nil {
		return "", errInvalidResponse
	}
	if err := validatePlannedAPObservation(&after.actual); err != nil {
		return "", err
	}
	view, err := plannedAPBaselineView(after)
	if err != nil {
		return "", err
	}
	expected, err := plannedAPNativeMAC(e.comparison, record.receipt.Operation.Resource, view)
	if err != nil {
		return "", err
	}
	_, prior, epoch, err := plannedAPCurrentAuthority(plannedAPResource(o.want), record, e.applied, e.floorEffects)
	if err != nil {
		return "", err
	}
	prefix, suffix := plannedAPPrivateUpdatePrefix, ""
	if epoch != "" {
		prefix, suffix = plannedAPFloorUpdatePrefix, ":"+epoch
	}
	return prefix + strings.Join(parts, ":") + ":" + prior + ":" + expected + suffix, nil
}
func plannedAPApplyAnchor(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (plannedAPIdentityRecord, error) {
	if !plannedAPAppliedSelected(op) || !siteDesignOperation(op) || op.Action != "Update" {
		return plannedAPIdentityRecord{}, errConfigurationTransition
	}
	parts, ok := plannedAPPrivateParts(op.Resource)
	records, err := plannedAPIdentityRecords(state)
	if err != nil {
		return plannedAPIdentityRecord{}, err
	}
	record, exists := records[plannedAPResourceKey(op.Resource)]
	base, valid := plannedAPPrivateParts(record.receipt.Operation.Resource)
	if !ok || !valid || !exists || !reflect.DeepEqual(parts[:3], base) || record.keyID == "" || op.TargetID != record.id || op.ParentID != record.receipt.Operation.ParentID || op.ConfigName != record.receipt.Operation.ConfigName || op.ConfigUID != record.receipt.Operation.ConfigUID || op.ConfigGeneration < record.receipt.Operation.ConfigGeneration || op.ControllerGeneration < record.receipt.Operation.ControllerGeneration {
		return plannedAPIdentityRecord{}, errConfigurationOwned
	}
	return record, nil
}
func plannedAPAppliedResult(op *cvk.NetworkControllerConfigurationOperation, record plannedAPIdentityRecord) string {
	parts, ok := plannedAPPrivateParts(op.Resource)
	if !ok || len(parts) < 5 {
		return ""
	}
	return plannedAPAppliedPrefix + parts[0] + ":" + parts[1] + ":" + op.TargetID + ":" + parts[2] + ":" + parts[4] + ":" + siteReceiptDigest(record.receipt)
}
func plannedAPAppliedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out, nil
	}
	floors, err := plannedAPFloorAuthorities(state)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, r := range state.ActionApplications {
		counts[r.Operation.ID]++
	}
	for _, r := range state.ActionApplications {
		if !strings.HasPrefix(r.Resource, plannedAPAppliedPrefix) && !plannedAPAppliedSelected(&r.Operation) {
			continue
		}
		if len(r.EffectReceipts) != 0 {
			return nil, errConfigurationOwned
		}
		op := &r.Operation
		record, err := plannedAPApplyAnchor(state, op)
		if err != nil || r.Section != "sites" || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || counts[op.ID] != 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.Before(record.receipt.VerifiedAt) || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || r.Resource != plannedAPAppliedResult(op, record) || (op.SourceUID == "") != (op.SourceVersion == "") {
			return nil, errConfigurationOwned
		}
		parts, _ := plannedAPPrivateParts(op.Resource)
		if (parts[1] == "c") != (op.SourceUID != "") {
			return nil, errConfigurationOwned
		}
		key := plannedAPResourceKey(op.Resource)
		if len(parts) == 6 {
			floor, ok := floors[key]
			if !ok || (parts[5] == floor.floor.Operation.ID && (op.SubmittedAt.Before(floor.floor.VerifiedAt) || op.ConfigGeneration < floor.floor.Operation.ConfigGeneration || op.ControllerGeneration < floor.floor.Operation.ControllerGeneration)) ||
				(parts[5] != floor.floor.Operation.ID && op.SubmittedAt.After(floor.floor.Operation.SubmittedAt.Time)) {
				return nil, errConfigurationOwned
			}
		}
		if _, exists := out[key]; exists {
			return nil, errConfigurationOwned
		}
		r.Operation = *r.Operation.DeepCopy()
		r.VerifiedAt = r.VerifiedAt.DeepCopy()
		out[key] = r
	}
	return out, nil
}
func plannedAPReplaceApplied(state *cvk.NetworkControllerConfigurationStatus, row cvk.NetworkControllerConfigurationActionApplication) error {
	key := plannedAPResourceKey(row.Operation.Resource)
	kept := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(state.ActionApplications)+1)
	for _, r := range state.ActionApplications {
		if strings.HasPrefix(r.Resource, plannedAPAppliedPrefix) && plannedAPResourceKey(r.Operation.Resource) == key {
			continue
		}
		kept = append(kept, r)
	}
	state.ActionApplications = append(kept, row)
	_, err := plannedAPAppliedRecords(state)
	return err
}
func plannedAPApplyCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	record, err := plannedAPApplyAnchor(state, op)
	if err != nil {
		return err
	}
	rows, err := plannedAPAppliedRecords(state)
	if err != nil {
		return err
	}
	floors, err := plannedAPFloorAuthorities(state)
	if err != nil {
		return err
	}
	_, prior, epoch, err := plannedAPCurrentAuthority(plannedAPResourceKey(op.Resource), record, rows, floors)
	if err != nil {
		return err
	}
	parts, _ := plannedAPPrivateParts(op.Resource)
	if parts[3] != prior || epoch != "" && (len(parts) != 6 || parts[5] != epoch) || epoch == "" && len(parts) != 5 {
		return errConfigurationStale
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID {
			return errConfigurationOwned
		}
	}
	future, at := op.DeepCopy(), metav1.Now()
	future.Phase, future.TaskID, future.SubmittedAt = "Verified", strings.Repeat("x", 128), &at
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPAppliedResult(future, record), Operation: *future, VerifiedAt: &at}
	working := state.DeepCopy()
	if err := plannedAPReplaceApplied(working, row); err != nil {
		return err
	}
	raw, err := json.Marshal(working.ActionApplications)
	if err != nil || len(working.ActionApplications) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}
func plannedAPAppliedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	rows, err := plannedAPAppliedRecords(state)
	if err != nil || op == nil {
		return false
	}
	row, ok := rows[plannedAPResourceKey(op.Resource)]
	if !ok || !reflect.DeepEqual(row.Operation, *op) {
		return false
	}
	floors, err := plannedAPFloorAuthorities(state)
	if err != nil {
		return false
	}
	if floor, exists := floors[plannedAPResourceKey(op.Resource)]; exists {
		parts, valid := plannedAPPrivateParts(op.Resource)
		return valid && len(parts) == 6 && parts[5] == floor.floor.Operation.ID
	}
	return true
}
func plannedAPAppliedSettlementRequired(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	if plannedAPAppliedSelected(op) {
		return true
	}
	if state != nil {
		for _, r := range state.ActionApplications {
			if r.Operation.ID == op.ID && (strings.HasPrefix(r.Resource, plannedAPAppliedPrefix) || plannedAPAppliedSelected(&r.Operation)) {
				return true
			}
		}
	}
	return false
}
