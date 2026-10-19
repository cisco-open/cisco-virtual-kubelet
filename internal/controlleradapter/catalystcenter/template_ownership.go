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
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const templateCreatePrefix = "template-create:v1:"
const templateCreatedPrefix = "template-created:v1:"

// Hash only public object identity, never template bodies or parameter values.
func templateCreatorResource(kind, project, name string) string {
	raw, _ := json.Marshal([]string{project, name})
	sum := sha256.Sum256(raw)
	return templateCreatePrefix + kind + ":" + hex.EncodeToString(sum[:])
}
func templateCreateKey(resource string) (string, bool) {
	if !strings.HasPrefix(resource, templateCreatePrefix) {
		return "", false
	}
	key := strings.TrimPrefix(resource, templateCreatePrefix)
	parts := strings.Split(key, ":")
	if (len(parts) != 3 && len(parts) != 5) || parts[0] != "p" && parts[0] != "r" && parts[0] != "c" || len(parts[1]) != 64 || strings.ToLower(parts[1]) != parts[1] || parts[2] != "i" && parts[2] != "c" {
		return "", false
	}
	if len(parts) == 5 && (parts[3] != templateComparisonTag || !validTemplateComparisonDigest(parts[4])) {
		return "", false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", false
	}
	return strings.Join(parts[:2], ":"), true
}
func canonicalTemplateUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}
func validTemplateCreateOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	key, ok := templateCreateKey(op.Resource)
	if !ok || op.Section != "templates" || op.Action != "Create" || op.TargetID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 {
		return false
	}
	// The source-mode discriminator detects schema loss of both source fields.
	// Empty phase/time is used only by write-shape eligibility before journaling.
	if op.Phase != "" || op.SubmittedAt != nil || op.SourceUID != "" || op.SourceVersion != "" {
		parts := strings.Split(strings.TrimPrefix(op.Resource, templateCreatePrefix), ":")
		fromConfigMap := parts[2] == "c"
		if fromConfigMap && (op.SourceUID == "" || op.SourceVersion == "") || !fromConfigMap && (op.SourceUID != "" || op.SourceVersion != "") {
			return false
		}
	}
	kind := strings.Split(key, ":")[0]
	if kind == "p" {
		return op.ParentID == "" && op.PreparedAt == nil
	}
	if !canonicalTemplateUUID(op.ParentID) || kind != "c" && op.PreparedAt != nil {
		return false
	}
	return op.PreparedAt == nil || !op.PreparedAt.IsZero() && (op.SubmittedAt == nil || !op.PreparedAt.After(op.SubmittedAt.Time))
}
func templateCreatedTarget(r cvk.NetworkControllerConfigurationActionApplication) (string, error) {
	op := &r.Operation
	key, ok := templateCreateKey(op.Resource)
	prefix := templateCreatedPrefix + key + ":"
	if !ok || !validTemplateCreateOperation(op) || op.Phase != "Verified" || r.Section != "templates" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || !strings.HasPrefix(r.Resource, prefix) {
		return "", errConfigurationOwned
	}
	parts := strings.Split(strings.TrimPrefix(r.Resource, prefix), ":")
	id := parts[0]
	if !canonicalTemplateUUID(id) {
		return "", errConfigurationOwned
	}
	comparisonKey := templateCreatorComparisonKey(op.Resource)
	if comparisonKey == "" {
		if len(parts) != 1 {
			return "", errConfigurationOwned
		}
	} else if len(parts) != 4 || parts[1] != templateComparisonTag || parts[2] != comparisonKey || !validTemplateComparisonDigest(parts[3]) {
		return "", errConfigurationOwned
	}
	return id, nil
}
func templateCreatedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out, nil
	}
	ids := map[string]bool{}
	for _, r := range state.ActionApplications {
		if !strings.HasPrefix(r.Resource, templateCreatedPrefix) && !strings.HasPrefix(r.Operation.Resource, templateCreatePrefix) {
			continue
		}
		id, err := templateCreatedTarget(r)
		if err != nil {
			return nil, err
		}
		key, _ := templateCreateKey(r.Operation.Resource)
		if _, duplicate := out[key]; duplicate || ids[id] {
			return nil, errConfigurationOwned
		}
		ids[id] = true
		out[key] = r
	}
	return out, nil
}
func templateCreateCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || !validTemplateCreateOperation(op) {
		return errConfigurationTransition
	}
	records, err := templateCreatedRecords(state)
	if err != nil {
		return err
	}
	key, _ := templateCreateKey(op.Resource)
	if _, exists := records[key]; exists {
		return errConfigurationOwned
	}
	// Reserve the shared count/byte limits before dispatch, with a worst-case task ID.
	future := *op.DeepCopy()
	future.Phase = "Verified"
	future.TaskID = strings.Repeat("x", 128)
	now := metav1.Now()
	future.SubmittedAt = &now
	receipts := append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...)
	result := templateCreatedPrefix + key + ":" + strings.Repeat("x", 36)
	if comparisonKey := templateCreatorComparisonKey(op.Resource); comparisonKey != "" {
		result += ":" + templateComparisonTag + ":" + comparisonKey + ":" + strings.Repeat("x", 64)
	}
	receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: result, Operation: future, VerifiedAt: &now})
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}
func templateCreatedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || !validTemplateCreateOperation(op) || op.Phase != "Verified" {
		return false
	}
	records, err := templateCreatedRecords(state)
	if err != nil {
		return false
	}
	key, _ := templateCreateKey(op.Resource)
	r, ok := records[key]
	if !ok || !reflect.DeepEqual(r.Operation, *op) {
		return false
	}
	count := 0
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID {
			count++
		}
	}
	return count == 1
}
func (c *client) templateCreateTaskResult(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (string, bool, error) {
	if c == nil || !validTemplateCreateOperation(op) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return "", false, errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+op.TaskID, nil, http.StatusOK)
	if err != nil {
		return "", false, err
	}
	done, err := decodeConfigurationTaskComplete(raw, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return "", false, err
	}
	var body struct {
		Response struct {
			Service string `json:"serviceType"`
			Data    string `json:"data"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response.Service != "NCTP" || !canonicalTemplateUUID(body.Response.Data) {
		return "", false, errInvalidResponse
	}
	return body.Response.Data, true, nil
}
