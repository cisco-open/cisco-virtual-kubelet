// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const metadataCredentialPrefix = "snmpv3-noauth:"

func (want globalCredentialIntent) metadataOnly() bool {
	return want.kind.model == "snmpv3_credentials" && want.kind.api == "snmpV3" && want.metadata.Mode == "NOAUTHNOPRIV" &&
		want.metadata.AuthType == "" && want.metadata.PrivacyType == "" && len(want.credentials) == 0
}

// The stable key binds the name. The operation additionally binds the selected
// non-secret payload. The receipt key adds the observed native UUID, without
// changing the immutable create operation's initially unknown target.
func (want globalCredentialIntent) metadataKey() string {
	return fmt.Sprintf("%s%x", metadataCredentialPrefix, sha256.Sum256([]byte(want.metadata.Name)))
}
func (want globalCredentialIntent) metadataResource() string {
	raw, _ := json.Marshal(globalCredentialMetadataPayload(want.metadata))
	return fmt.Sprintf("%s:%x", want.metadataKey(), sha256.Sum256(raw))
}
func metadataCredentialKey(resource string) (string, bool) {
	parts := strings.Split(resource, ":")
	if len(parts) != 3 || parts[0]+":" != metadataCredentialPrefix || len(parts[1]) != 64 || len(parts[2]) != 64 {
		return "", false
	}
	for _, part := range parts[1:] {
		if _, err := hex.DecodeString(part); err != nil || strings.ToLower(part) != part {
			return "", false
		}
	}
	return metadataCredentialPrefix + parts[1], true
}
func isMetadataCredentialOperation(section, resource string) bool {
	return section == "network_settings" && strings.HasPrefix(resource, metadataCredentialPrefix)
}

func validMetadataCredentialOperation(op *cvk.NetworkControllerConfigurationOperation, phase string, now time.Time) bool {
	if op == nil {
		return false
	}
	_, valid := metadataCredentialKey(op.Resource)
	return valid && op.Section == "network_settings" && op.Phase == phase && op.ConfigName != "" && op.ConfigUID != "" &&
		op.ConfigGeneration > 0 && op.ControllerGeneration > 0 && configurationOperationID.MatchString(op.ID) && validAPIID(op.TaskID) &&
		(op.Action == "Create" && op.TargetID == "" || op.Action == "Update" && validAPIID(op.TargetID)) &&
		op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && !op.SubmittedAt.After(now) && op.ParentID == "" && op.TargetSerial == "" &&
		op.CreatedResourceID == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.CancellationRequestedAt == nil && op.PreparedAt == nil && len(op.SSIDCredentialHistory) == 0 && len(op.CredentialVersions) == 0
}

func metadataCredentialReceiptTarget(receipt *cvk.NetworkControllerConfigurationActionApplication, now time.Time) (string, error) {
	if receipt == nil || !validMetadataCredentialOperation(&receipt.Operation, "Verified", now) || receipt.Section != "network_settings" ||
		receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.After(now) || receipt.VerifiedAt.Before(receipt.Operation.SubmittedAt) {
		return "", errConfigurationOwned
	}
	key, _ := metadataCredentialKey(receipt.Operation.Resource)
	if !strings.HasPrefix(receipt.Resource, key+":") {
		return "", errConfigurationOwned
	}
	target := strings.TrimPrefix(receipt.Resource, key+":")
	if !validAPIID(target) || receipt.Operation.TargetID != "" && receipt.Operation.TargetID != target {
		return "", errConfigurationOwned
	}
	return target, nil
}

func (e *globalCredentialExecutor) metadataCredentialReceipt(want globalCredentialIntent) (*cvk.NetworkControllerConfigurationActionApplication, string, error) {
	if want.kind.model != "snmpv3_credentials" {
		return nil, "", nil
	}
	var found *cvk.NetworkControllerConfigurationActionApplication
	var target string
	for i := range e.metadataReceipts {
		receipt := &e.metadataReceipts[i]
		if receipt.Section != "network_settings" || !strings.HasPrefix(receipt.Resource, want.metadataKey()+":") {
			continue
		}
		id, err := metadataCredentialReceiptTarget(receipt, time.Now())
		if err != nil || found != nil || receipt.Operation.ConfigName != e.configName || receipt.Operation.ConfigUID != e.owner {
			return nil, "", errConfigurationOwned
		}
		found, target = receipt, id
	}
	return found, target, nil
}

func (e *globalCredentialExecutor) nextMetadataCredential(want globalCredentialIntent, actual map[string]map[string]globalCredentialMetadata) (*configurationWrite, error) {
	if !want.metadataOnly() || e.owner == "" || e.configName == "" {
		return nil, errConfigurationTransition
	}
	for _, receipt := range e.receipts {
		if receipt.Section == "network_settings" && receipt.Resource == want.resource() {
			return nil, errConfigurationOwned
		}
	}
	prior, id, err := e.metadataCredentialReceipt(want)
	if err != nil {
		return nil, err
	}
	if prior == nil && len(e.metadataReceipts) >= 256 {
		return nil, errConfigurationTransition
	}
	got, exists := actual[want.kind.model][want.metadata.Name]
	if exists && (prior == nil || got.ID != id) || !exists && prior != nil {
		return nil, errConfigurationOwned
	}
	if exists && (got.Mode != "NOAUTHNOPRIV" || got.AuthType != "" || got.PrivacyType != "") {
		return nil, errConfigurationOwned
	}
	if exists && globalCredentialMatches(want.metadata, got) {
		return nil, nil
	}
	payload := globalCredentialMetadataPayload(want.metadata)
	method, action := http.MethodPost, "Create"
	var body any = map[string]any{want.kind.api: []any{payload}}
	if exists {
		method, action = http.MethodPut, "Update"
		payload["id"] = id
		body = map[string]any{want.kind.api: payload}
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &configurationWrite{resource: want.metadataResource(), action: action, identity: configurationResourceIdentity{target: id}, submit: func(ctx context.Context) (Task, error) {
		raw, err := e.client.mutate(ctx, method, globalCredentialsPath, rawBody)
		if err != nil {
			return Task{}, err
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: ack.Response.ID}, nil
	}}, nil
}

// Select observation authority independently of native name readback. An
// acknowledged create has no prior receipt; an update must retain its exact UUID.
func (e *globalCredentialExecutor) knownMetadataCredentialTask(op *cvk.NetworkControllerConfigurationOperation, now time.Time) bool {
	if e == nil || op == nil || (op.SourceUID == "") != (op.SourceVersion == "") || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || !validMetadataCredentialOperation(op, op.Phase, now) || op.ConfigUID != e.owner || op.ConfigName != e.configName {
		return false
	}
	matched := false
	for _, want := range e.desired {
		if !want.metadataOnly() || want.metadataResource() != op.Resource {
			continue
		}
		if matched {
			return false
		}
		prior, target, err := e.metadataCredentialReceipt(want)
		if err != nil || op.Action == "Create" && prior != nil || op.Action == "Update" && (prior == nil || target != op.TargetID || prior.Operation.ID == op.ID) {
			return false
		}
		for _, receipt := range e.receipts {
			if receipt.Section == "network_settings" && receipt.Resource == want.resource() {
				return false
			}
		}
		matched = true
	}
	return matched
}

// Task-tree completion and repeated native metadata readback are both required.
// Recovery cannot adopt an existing definition merely because its name matches.
func (e *globalCredentialExecutor) verifyMetadataCredential(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (string, bool, error) {
	if op == nil || op.Phase != "Verifying" || !e.knownMetadataCredentialTask(op, now) {
		return "", false, errConfigurationTransition
	}
	var want *globalCredentialIntent
	for i := range e.desired {
		if e.desired[i].metadataOnly() && e.desired[i].metadataResource() == op.Resource {
			if want != nil {
				return "", false, errConfigurationTransition
			}
			want = &e.desired[i]
		}
	}
	if want == nil {
		return "", false, errConfigurationStale
	}
	prior, target, err := e.metadataCredentialReceipt(*want)
	if err != nil || op.Action == "Create" && prior != nil || op.Action == "Update" && (prior == nil || target != op.TargetID) {
		return "", false, errConfigurationOwned
	}
	for _, receipt := range e.receipts {
		if receipt.Section == "network_settings" && receipt.Resource == want.resource() {
			return "", false, errConfigurationOwned
		}
	}
	completed, err := e.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || completed.IsZero() || completed.After(now) {
		return "", false, err
	}
	var first globalCredentialMetadata
	for read := 0; read < 2; read++ {
		actual, err := e.client.globalCredentials(ctx)
		if err != nil {
			return "", false, err
		}
		got, exists := actual[want.kind.model][want.metadata.Name]
		if !exists || !globalCredentialMatches(want.metadata, got) || got.Mode != "NOAUTHNOPRIV" || got.AuthType != "" || got.PrivacyType != "" || target != "" && got.ID != target {
			return "", false, nil
		}
		if read == 0 {
			first = got
		} else if !reflect.DeepEqual(first, got) {
			return "", false, errConfigurationStale
		}
	}
	final, err := e.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !final.Equal(completed) {
		return "", false, errConfigurationStale
	}
	return first.ID, true, nil
}
