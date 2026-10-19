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

const ipamOwnedPrefix = "ipam-owned:v1:"
const ipamResultPrefix = "ipam-result:v1:"

func ipamOwnedResource(legacy string) string {
	kind := "pool"
	if strings.HasPrefix(legacy, "ip-reservation:") {
		kind = "reservation"
	} else if !strings.HasPrefix(legacy, "ip-pool:") {
		return ""
	}
	hash := sha256.Sum256([]byte(legacy))
	return ipamOwnedPrefix + kind + ":" + hex.EncodeToString(hash[:])
}
func ipamOwnedKey(resource string) (string, bool) {
	resource = strings.TrimPrefix(resource, "network_settings/")
	if !strings.HasPrefix(resource, ipamOwnedPrefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(resource, ipamOwnedPrefix), ":")
	if len(parts) != 2 || parts[0] != "pool" && parts[0] != "reservation" || len(parts[1]) != 64 || strings.ToLower(parts[1]) != parts[1] {
		return "", false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", false
	}
	return strings.TrimPrefix(resource, ipamOwnedPrefix), true
}
func ipamOwnedSelected(resource string) bool {
	return strings.HasPrefix(resource, ipamOwnedPrefix) || strings.HasPrefix(resource, "network_settings/"+ipamOwnedPrefix)
}
func validIPAMOwnedOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	key, ok := ipamOwnedKey(op.Resource)
	if !ok || (op.Section != "network_settings" && op.Section != "fabric") || op.Section == "network_settings" && strings.Contains(op.Resource, "/") || op.Section == "fabric" && !strings.HasPrefix(op.Resource, "network_settings/") {
		return false
	}
	if op.TargetSerial != "" || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || op.DependencyVersions != nil || op.CreatedResourceID != "" || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 {
		return false
	}
	if op.Action != "Create" && op.Action != "Update" && op.Action != "Delete" || op.Action == "Create" && op.TargetID != "" || (op.Action == "Update" || op.Action == "Delete") && !canonicalIPAMUUID(op.TargetID) {
		return false
	}
	return strings.HasPrefix(key, "pool:") && op.ParentID == "" || strings.HasPrefix(key, "reservation:") && validAPIID(op.ParentID)
}
func ipamResultKey(resource, id string) string {
	key, ok := ipamOwnedKey(resource)
	if !ok {
		return ""
	}
	return ipamResultPrefix + key + ":" + id
}
func canonicalIPAMUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}
func ipamReceiptTarget(r cvk.NetworkControllerConfigurationActionApplication) (string, error) {
	op := &r.Operation
	if !validIPAMOwnedOperation(op) || op.Phase != "Verified" || r.Section != op.Section || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) {
		return "", errConfigurationOwned
	}
	prefix := ipamResultKey(op.Resource, "")
	id := strings.TrimPrefix(r.Resource, prefix)
	if !strings.HasPrefix(r.Resource, prefix) || !canonicalIPAMUUID(id) || (op.Action == "Update" || op.Action == "Delete") && op.TargetID != id {
		return "", errConfigurationOwned
	}
	return id, nil
}
func (e *ipamConfigurationExecutor) ipamReceipt(legacy string) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	prefix := ipamResultKey(ipamOwnedResource(legacy), "")
	if prefix == "" {
		return nil, errConfigurationTransition
	}
	var found *cvk.NetworkControllerConfigurationActionApplication
	for i := range e.receipts {
		r := &e.receipts[i]
		if !strings.HasPrefix(r.Resource, prefix) {
			continue
		}
		if found != nil || r.Operation.ConfigUID != e.owner {
			return nil, errConfigurationOwned
		}
		if _, err := ipamReceiptTarget(*r); err != nil {
			return nil, err
		}
		found = r
	}
	return found, nil
}
func (e *ipamConfigurationExecutor) legacyOwnedResource(resource string) string {
	key, ok := ipamOwnedKey(resource)
	if !ok {
		return ""
	}
	for _, p := range e.intent.pools {
		legacy := ipamPoolIdentity(p.Name)
		k, _ := ipamOwnedKey(ipamOwnedResource(legacy))
		if k == key {
			return legacy
		}
	}
	for _, r := range e.intent.reservations {
		legacy := ipamReservationIdentity(r.site, r.Name)
		k, _ := ipamOwnedKey(ipamOwnedResource(legacy))
		if k == key {
			return legacy
		}
	}
	if e.prune {
		for _, r := range e.receipts {
			if k, ok := ipamOwnedKey(r.Operation.Resource); ok && k == key {
				return "owned-prune:" + key
			}
		}
	}
	return ""
}
func (e *ipamConfigurationExecutor) checkOwnership(pools []ipPool, reservations []ipReservation) error {
	if e.owner == "" {
		return nil
	}
	check := func(legacy, name string, ids, parents map[string]string) error {
		receipt, err := e.ipamReceipt(legacy)
		if err != nil || receipt == nil {
			return err
		}
		id, err := ipamReceiptTarget(*receipt)
		if err != nil {
			return err
		}
		if receipt.Operation.Action == "Delete" {
			if ids[name] != "" {
				return errConfigurationOwned
			}
			for _, other := range ids {
				if other == id {
					return errConfigurationOwned
				}
			}
			return nil
		}
		if ids[name] != id || parents != nil && receipt.Operation.ParentID != parents[name] {
			return errConfigurationOwned
		}
		for n, other := range ids {
			if other == id && n != name {
				return errConfigurationOwned
			}
		}
		return nil
	}
	ps := map[string]string{}
	rs := map[string]string{}
	parentSites := map[string]string{}
	for _, p := range pools {
		ps[p.Name] = p.ID
	}
	for _, r := range reservations {
		rs[r.Name] = r.ID
		parentSites[r.Name] = r.SiteID
	}
	for _, p := range e.intent.pools {
		if err := check(ipamPoolIdentity(p.Name), p.Name, ps, nil); err != nil {
			return err
		}
	}
	for _, r := range e.intent.reservations {
		if err := check(ipamReservationIdentity(r.site, r.Name), r.Name, rs, parentSites); err != nil {
			return err
		}
	}
	return nil
}

// NCIP returns a bare UUID, not arbitrary prose or a JSON deployment identifier.
// Validate the same complete task observation before interpreting its progress.
func (c *client) ipamTaskResult(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, bool, error) {
	if !validIPAMOwnedOperation(op) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return "", false, errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+op.TaskID, nil, http.StatusOK)
	if err != nil {
		return "", false, err
	}
	done, err := decodeConfigurationTaskComplete(raw, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil || !done {
		return "", false, err
	}
	var body struct {
		Response struct {
			Service  string `json:"serviceType"`
			Progress string `json:"progress"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response.Service != "NCIP" || !canonicalIPAMUUID(body.Response.Progress) {
		return "", false, errInvalidResponse
	}
	return body.Response.Progress, true, nil
}
func (e *ipamConfigurationExecutor) verifyOwned(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, bool, error) {
	if !validIPAMOwnedOperation(op) || op.ConfigUID != e.owner {
		return "", false, errConfigurationTransition
	}
	if op.Action == "Delete" {
		return e.verifyDelete(ctx, op)
	}
	legacy := e.legacyOwnedResource(op.Resource)
	if legacy == "" {
		return "", false, errConfigurationTransition
	}
	id, done, err := e.client.ipamTaskResult(ctx, op)
	if err != nil || !done {
		return "", false, err
	}
	if op.Action == "Update" && id != op.TargetID {
		return "", false, errConfigurationOwned
	}
	original := op.DeepCopy()
	original.Resource = legacy
	original.TargetID = id
	// The existing verifier checks the original site and both selected parent
	// families/fields; explicit target binding also protects Create from adoption.
	verifier := e
	receipt, receiptErr := e.ipamReceipt(legacy)
	if receiptErr != nil {
		return "", false, receiptErr
	}
	if receipt != nil && receipt.Operation.Action == "Delete" {
		if op.Action != "Create" || !configurationInputAfter(op, &receipt.Operation) {
			return "", false, errConfigurationOwned
		}
		copy := *e
		copy.receipts = nil
		prefix := ipamResultKey(op.Resource, "")
		for _, r := range e.receipts {
			if !strings.HasPrefix(r.Resource, prefix) {
				copy.receipts = append(copy.receipts, r)
			}
		}
		verifier = &copy
	}
	done, err = verifier.verify(ctx, original)
	return id, done, err
}
func ipamOwnedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || !validIPAMOwnedOperation(op) || op.Phase != "Verified" {
		return false
	}
	count := 0
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID || strings.HasPrefix(r.Resource, ipamResultKey(op.Resource, "")) {
			count++
			if _, err := ipamReceiptTarget(r); err != nil || !reflect.DeepEqual(r.Operation, *op) {
				return false
			}
		}
	}
	return count == 1
}
func ipamOwnedCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || !validIPAMOwnedOperation(op) {
		return errConfigurationTransition
	}
	prefix := ipamResultKey(op.Resource, "")
	receipts := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(state.ActionApplications)+1)
	found := false
	for _, r := range state.ActionApplications {
		if strings.HasPrefix(r.Resource, prefix) {
			id, err := ipamReceiptTarget(r)
			if found || err != nil || r.Operation.ConfigUID != op.ConfigUID {
				return errConfigurationOwned
			}
			if op.Action == "Create" {
				if r.Operation.Action != "Delete" || !configurationInputAfter(op, &r.Operation) {
					return errConfigurationOwned
				}
			} else if r.Operation.Action == "Delete" || id != op.TargetID || r.Operation.ParentID != op.ParentID {
				return errConfigurationOwned
			}
			found = true
		} else {
			receipts = append(receipts, r)
		}
	}
	if (op.Action == "Update" || op.Action == "Delete") && !found {
		return errConfigurationOwned
	}
	result := *op.DeepCopy()
	result.Phase = "Verified"
	result.TaskID = strings.Repeat("x", 128)
	now := metav1.Now()
	result.SubmittedAt = &now
	receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: prefix + strings.Repeat("x", 128), Operation: result, VerifiedAt: &now})
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errConfigurationTransition
	}
	return nil
}
