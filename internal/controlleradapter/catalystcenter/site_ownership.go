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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const siteCreatedPrefix = "site-created:v1:"

func siteCreateSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	return op != nil && op.Section == "sites" && op.Action == "Create" && (validSitePath(op.Resource) && op.Resource != "Global" || (strings.HasPrefix(op.Resource, sitePrivateCreatePrefix) || siteRecreateSelected(op.Resource)))
}

// Only public identity is hashed. Site attributes are never published here.
func siteCreatedResource(op *cvk.NetworkControllerConfigurationOperation, kind, id string, baselines ...string) string {
	if strings.HasPrefix(op.Resource, sitePrivateCreatePrefix) || siteRecreateSelected(op.Resource) {
		parts, ok := sitePrivateCreationParts(op.Resource)
		if !ok || parts[0] != kind || len(baselines) != 1 || !validTemplateComparisonDigest(baselines[0]) {
			return ""
		}
		return sitePrivateCreatedPrefix + kind + ":" + parts[1] + ":" + parts[2] + ":" + id + ":" + parts[3] + ":" + baselines[0]
	}
	mode := "i"
	if op.SourceUID != "" {
		mode = "c"
	}
	sum := sha256.Sum256([]byte(op.Resource))
	return siteCreatedPrefix + kind + ":" + hex.EncodeToString(sum[:]) + ":" + mode + ":" + id
}

type siteCreatorRecord struct {
	kind, id   string
	keyID, mac string
	receipt    cvk.NetworkControllerConfigurationActionApplication
}

func siteCreatedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]siteCreatorRecord, error) {
	rows, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	if _, err := siteHistoryCohorts(state); err != nil {
		return nil, err
	}
	return rows, nil
}

func siteCreatedRecordsCurrent(state *cvk.NetworkControllerConfigurationStatus) (map[string]siteCreatorRecord, error) {
	out, ids, operations := map[string]siteCreatorRecord{}, map[string]bool{}, map[string]bool{}
	identities := map[string]bool{}
	if state == nil {
		return out, nil
	}
	counts := map[string]int{}
	for _, r := range state.ActionApplications {
		counts[r.Operation.ID]++
	}
	for _, r := range state.ActionApplications {
		if strings.HasPrefix(r.Resource, "site-history:") {
			continue
		}
		if !strings.HasPrefix(r.Resource, siteCreatedPrefix) && !strings.HasPrefix(r.Resource, sitePrivateCreatedPrefix) && !siteCreateSelected(&r.Operation) {
			continue
		}
		op := &r.Operation
		parts := strings.Split(strings.TrimPrefix(r.Resource, siteCreatedPrefix), ":")
		private, keyID, mac := strings.HasPrefix(r.Resource, sitePrivateCreatedPrefix), "", ""
		if private {
			parts = strings.Split(strings.TrimPrefix(r.Resource, sitePrivateCreatedPrefix), ":")
		}
		if private && len(parts) == 6 {
			keyID, mac = parts[4], parts[5]
		}
		result := ""
		if len(parts) >= 4 {
			if private {
				result = siteCreatedResource(op, parts[0], parts[3], mac)
			} else {
				result = siteCreatedResource(op, parts[0], parts[3])
			}
		}
		if (!private && len(parts) != 4 || private && (len(parts) != 6 || !validTemplateComparisonDigest(keyID) || !validTemplateComparisonDigest(mac))) || parts[0] != "area" && parts[0] != "building" && parts[0] != "floor" || !canonicalTemplateUUID(parts[3]) ||
			!siteSingularOperation(op) || !siteCreateSelected(op) || op.Phase != "Verified" || r.Section != "sites" || len(r.EffectReceipts) != 0 ||
			!configurationOperationID.MatchString(op.ID) || op.ConfigName == "" || op.ConfigUID == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 ||
			!validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) ||
			(op.SourceUID == "") != (op.SourceVersion == "") || result == "" || r.Resource != result {
			return nil, errConfigurationOwned
		}
		if _, duplicate := out[op.Resource]; duplicate || identities[parts[1]] || ids[parts[3]] || operations[op.ID] || counts[op.ID] != 1 {
			return nil, errConfigurationOwned
		}
		ids[parts[3]], operations[op.ID], identities[parts[1]] = true, true, true
		r.Operation = *r.Operation.DeepCopy()
		r.VerifiedAt = r.VerifiedAt.DeepCopy()
		out[op.Resource] = siteCreatorRecord{kind: parts[0], id: parts[3], keyID: keyID, mac: mac, receipt: r}
	}
	return out, nil
}

func siteCreatedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if !siteCreateSelected(op) || op.Phase != "Verified" {
		return false
	}
	records, err := siteCreatedRecords(state)
	if err != nil {
		return false
	}
	r, ok := records[op.Resource]
	if !ok || !reflect.DeepEqual(r.receipt.Operation, *op) {
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

func siteCreateCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || !siteSingularOperation(op) || !siteCreateSelected(op) {
		return errConfigurationTransition
	}
	records, err := siteCreatedRecords(state)
	if err != nil {
		return err
	}
	if siteRecreateSelected(op.Resource) {
		// Budget the archive envelopes too, without changing the durable journal
		// or requiring a submission phase during planning.
		planning := state.DeepCopy()
		planning.Operation = op.DeepCopy()
		planning.Operation.Phase = "Submitting"
		state, err = siteArchivePredecessor(planning, planning.Operation)
		if err != nil {
			return err
		}
		records = nil // The complete predecessor is now archived in the copy.
	}
	for key := range records {
		if siteResourcePathDigest(key) == siteResourcePathDigest(op.Resource) {
			return errConfigurationOwned
		}
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID {
			return errConfigurationOwned
		}
	}
	future, now := op.DeepCopy(), metav1.Now()
	future.Phase, future.TaskID, future.SubmittedAt = "Verified", strings.Repeat("x", 128), &now
	receipts := append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...)
	result := siteCreatedResource(future, "building", strings.Repeat("x", 36))
	if parts, private := sitePrivateCreationParts(op.Resource); private {
		result = siteCreatedResource(future, parts[0], strings.Repeat("x", 36), strings.Repeat("a", 64))
	}
	receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: result, Operation: *future, VerifiedAt: &now})
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}

// Retained lineage fences missing/replaced declared objects. It is not a
// latest-applied attribute baseline or authority to prune retained sites.
func (e *siteConfigurationExecutor) checkCreatorIdentities(actual []Site) error {
	for _, desired := range e.desired {
		record, exists := siteCreatorRecord{}, false
		for key, candidate := range e.creators {
			if _, ok := siteDesiredOperation([]desiredSite{desired}, key); ok {
				record, exists = candidate, true
				break
			}
		}
		if !exists {
			continue
		}
		if record.receipt.Operation.ConfigUID != e.creatorOwner || record.kind != desired.kind {
			return errConfigurationOwned
		}
		found := false
		for _, site := range actual {
			if site.NameHierarchy == desired.path {
				if site.ID != record.id || site.ParentID != record.receipt.Operation.ParentID || site.Type != record.kind {
					return errConfigurationOwned
				}
				found = true
			}
		}
		if !found {
			return errConfigurationOwned
		}
	}
	return nil
}

func (e *siteConfigurationExecutor) creatorRecordsMatch(state *cvk.NetworkControllerConfigurationStatus) error {
	current, err := siteCreatedRecords(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, e.creators) {
		return errConfigurationStale
	}
	applied, err := siteAppliedRecords(state)
	if err != nil {
		return err
	}
	if !(len(applied) == 0 && len(e.applied) == 0) && !reflect.DeepEqual(applied, e.applied) {
		return errConfigurationStale
	}
	return nil
}

func (e *siteConfigurationExecutor) verifyCreator(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, []byte, error) {
	if !siteSingularOperation(op) || !siteCreateSelected(op) || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
		return "", nil, errConfigurationTransition
	}
	done, err := e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return "", nil, err
	}
	if !done {
		return "", nil, errConfigurationBusy
	}
	raw, err := e.client.get(ctx, taskPath+op.TaskID, nil, http.StatusOK)
	if err != nil {
		return "", nil, err
	}
	done, err = decodeConfigurationTaskComplete(raw, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return "", nil, err
	}
	if !done {
		return "", nil, errConfigurationBusy
	}
	var body struct {
		Response struct {
			Data string `json:"data"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || !canonicalTemplateUUID(body.Response.Data) {
		return "", nil, errInvalidResponse
	}
	actual, err := e.client.ListSites(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, desired := range e.desired {
		if _, ok := siteDesiredOperation([]desiredSite{desired}, op.Resource); !ok {
			continue
		}
		for _, site := range actual {
			if site.NameHierarchy != desired.path {
				continue
			}
			if site.ID != body.Response.Data || site.Type != desired.kind || site.ParentID != op.ParentID {
				return "", nil, errConfigurationOwned
			}
			drift, err := planSites([]desiredSite{desired}, actual)
			if err != nil {
				return "", nil, err
			}
			if len(drift) != 0 {
				return "", nil, errConfigurationBusy
			}
			view, err := siteDispatchFootprint(actual, desired.path)
			if _, private := sitePrivateCreationParts(op.Resource); private {
				if e.comparison == nil || e.comparison.check == nil {
					return "", nil, errConfigurationSecret
				}
				if err := e.comparison.check(ctx); err != nil {
					return "", nil, err
				}
				view, err = e.client.siteNativeFootprint(ctx, actual, desired.path, false)
				if err != nil {
					return "", nil, err
				}
				if _, err := siteNativeMAC(e.comparison, op.Resource, view); err != nil {
					return "", nil, err
				}
				if err := e.comparison.check(ctx); err != nil {
					return "", nil, err
				}
			}
			return site.ID, view, err
		}
	}
	return "", nil, errConfigurationBusy
}

func (a *adapter) recordSiteCreated(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *siteConfigurationExecutor, op *cvk.NetworkControllerConfigurationOperation) error {
	if !siteCreateSelected(op) || op.Phase != "Verifying" || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
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
	if err := siteCreateCapacity(current.Status.Configuration, op); err != nil {
		return err
	}
	id, view, err := e.verifyCreator(ctx, op)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	again, fresh, err := e.verifyCreator(ctx, op)
	if err != nil {
		return err
	}
	if again != id || !reflect.DeepEqual(view, fresh) {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	kind := ""
	for _, desired := range e.desired {
		if _, ok := siteDesiredOperation([]desiredSite{desired}, op.Resource); ok {
			kind = desired.kind
		}
	}
	if kind == "" {
		return errConfigurationStale
	}
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if op.SubmittedAt.After(now.Time) {
		now = *op.SubmittedAt.DeepCopy()
	}
	result := siteCreatedResource(op, kind, id)
	if _, private := sitePrivateCreationParts(op.Resource); private {
		mac, err := siteNativeMAC(e.comparison, op.Resource, view)
		if err != nil {
			return err
		}
		result = siteCreatedResource(op, kind, id, mac)
	}
	if result == "" {
		return errConfigurationTransition
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	publication, err := siteCreationPublication(current.Status.Configuration, op, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: result, Operation: *verified, VerifiedAt: &now})
	if err != nil {
		return err
	}
	current.Status.Configuration = publication
	return a.persistSiteCreationPublication(ctx, current)
}
