// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const discoveryRetirementCondition = "DiscoveryOwnershipRetirement"

// Completion is an attestation by the deleting worker, not permission to submit.
// Ownership remains reserved until the original config UID has disappeared and
// another enrolled pure-discovery config independently verifies native absence.
// Retained action receipts are never removed or relabeled during handoff.
type discoveryOwnershipRetirement struct {
	ConfigName              string
	Completion              discoveryConfigCompletion
	RetainedDigest          string
	RetiredDigest           string
	CredentialHistoryDigest string                                       `json:"CredentialHistoryDigest,omitempty"`
	CredentialCustody       *discoveryCredentialCustody                  `json:"CredentialCustody,omitempty"`
	UnsubmittedOperation    *cvk.NetworkControllerConfigurationOperation `json:"UnsubmittedOperation,omitempty"`
}

func discoveryRetirementEvidence(nc *cvk.NetworkController, name, uid string, generation int64) (*cvk.NetworkControllerConfigurationStatus, error) {
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || name == "" || uid == "" || generation < 1 {
		return nil, errConfigurationBusy
	}
	c := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid), Generation: generation}, Spec: configapi.NetworkControllerConfigSpec{ManagedSections: []string{"inventory"}}}
	if discoveryCredentialHistoryOwner(nc, c) {
		return discoveryCredentialClosedEvidence(nc, c)
	}
	if !discoveryConfigKnownWorkClosed(nc, c) {
		return nil, errConfigurationOwned
	}
	selected := &cvk.NetworkControllerConfigurationStatus{}
	for _, o := range nc.Status.Configuration.Owners {
		if o.ConfigUID == uid {
			selected.Owners = append(selected.Owners, o)
		}
	}
	if len(selected.Owners) != 1 || selected.Owners[0].Section != "inventory" || selected.Owners[0].ConfigName != name {
		return nil, errConfigurationOwned
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if r.Operation.ConfigUID == uid {
			selected.ActionApplications = append(selected.ActionApplications, r)
		}
	}
	return selected, nil
}

// Called only after the same repeated native completion observation and fresh
// source/spec/evidence checks used to publish the config's completion proof.
func (a *adapter) retainDiscoveryRetirement(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, proof *discoveryConfigCompletion) error {
	if strings.HasPrefix(proof.CredentialDigest, discoveryCredentialUnsubmittedPrefix) {
		record, err := discoveryCredentialUnsubmittedRecord(nc, c)
		if err != nil || !reflect.DeepEqual(record.Completion, *proof) {
			return errConfigurationOwned
		}
		return nil
	}
	if strings.HasPrefix(proof.CredentialDigest, discoveryCredentialCompletionPrefix) && discoveryCredentialCustodyRequired(nc, string(c.UID)) {
		if _, err := discoveryCredentialCleanupHistory(nc, c, nil, nil); err != nil {
			return err
		}
	}
	selected, err := discoveryRetirementEvidence(nc, c.Name, string(c.UID), c.Generation)
	if err != nil {
		// Empty/unowned completion never creates retirement authority.
		if nc.Status.Configuration == nil {
			return nil
		}
		owned := false
		for _, o := range nc.Status.Configuration.Owners {
			owned = owned || o.ConfigUID == string(c.UID)
		}
		for _, r := range nc.Status.Configuration.ActionApplications {
			owned = owned || r.Operation.ConfigUID == string(c.UID)
		}
		if !owned {
			return nil
		}
		return err
	}
	if !reflect.DeepEqual(discoveryCompletionIdentity(nc, c), discoveryConfigCompletion{ControllerUID: proof.ControllerUID, ConfigUID: proof.ConfigUID, ControllerGeneration: proof.ControllerGeneration, ConfigGeneration: proof.ConfigGeneration, SpecDigest: proof.SpecDigest, DeletionDigest: proof.DeletionDigest, EvidenceDigest: proof.EvidenceDigest}) {
		return errConfigurationStale
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) && r.Operation.ConfigUID != string(c.UID) {
			if r.Operation.Action != "Delete" || !strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) {
				continue
			}
			if !discoveryRetiredReceiptKeys(nc, string(c.UID))[r.Resource] {
				return errConfigurationOwned
			}
		}
	}
	all, err := discoveryRetiredRows(nc, "")
	if err != nil {
		return err
	}
	record := discoveryOwnershipRetirement{ConfigName: c.Name, Completion: *proof, RetainedDigest: featureIdentityDigest(selected), RetiredDigest: featureIdentityDigest(all)}
	if strings.HasPrefix(proof.CredentialDigest, discoveryCredentialCompletionPrefix) {
		cohort, err := discoveryCredentialHistoryCohort(nc.Status.Configuration, string(c.UID))
		if err != nil {
			return err
		}
		record.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + cohort.allDigest
	}
	raw, _ := json.Marshal(record)
	old := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if old != nil && old.Status == metav1.ConditionTrue && old.Reason == "DiscoveryConfigClosed" && old.ObservedGeneration == nc.Generation && old.Message == string(raw) {
		return nil
	}
	meta.SetStatusCondition(&nc.Status.Conditions, condition(discoveryRetirementCondition, true, "DiscoveryConfigClosed", string(raw), nc.Generation))
	return a.statusWriter.Status().Update(ctx, nc)
}

func decodeDiscoveryRetirement(nc *cvk.NetworkController) (*discoveryOwnershipRetirement, error) {
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DiscoveryConfigClosed" || cond.ObservedGeneration != nc.Generation {
		return nil, errConfigurationOwned
	}
	var r discoveryOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &r) != nil {
		return nil, errConfigurationOwned
	}
	// Ordinary handoff cannot consume typed credential retirement. The audit
	// remains reserved until that separate migration path is qualified.
	if r.Completion.CredentialDigest != "" || r.UnsubmittedOperation != nil || r.CredentialHistoryDigest != "" || r.CredentialCustody != nil {
		return nil, errConfigurationOwned
	}
	raw, _ := json.Marshal(r)
	p := r.Completion
	if string(raw) != cond.Message || p.ControllerUID != string(nc.UID) || p.ControllerGeneration != nc.Generation || p.ConfigUID == "" || p.ConfigGeneration < 1 || (p.SourceUID == "") != (p.SourceVersion == "") {
		return nil, errConfigurationOwned
	}
	for _, digest := range []string{p.SpecDigest, p.DeletionDigest, p.EvidenceDigest, p.ModelDigest, r.RetainedDigest, r.RetiredDigest} {
		if !networkSettingDigestValid(digest) {
			return nil, errConfigurationOwned
		}
	}
	if p.ScopeDigest != "" && !networkSettingDigestValid(p.ScopeDigest) {
		return nil, errConfigurationOwned
	}
	return &r, nil
}

func discoveryRetirementRecord(nc *cvk.NetworkController) (*discoveryOwnershipRetirement, *cvk.NetworkControllerConfigurationStatus, error) {
	r, err := decodeDiscoveryRetirement(nc)
	if err != nil {
		return nil, nil, err
	}
	p := r.Completion
	selected, err := discoveryRetirementEvidence(nc, r.ConfigName, p.ConfigUID, p.ConfigGeneration)
	if err != nil || featureIdentityDigest(selected) != r.RetainedDigest {
		return nil, nil, errConfigurationOwned
	}
	all, err := discoveryRetiredRows(nc, "")
	if err != nil || featureIdentityDigest(all) != r.RetiredDigest {
		return nil, nil, errConfigurationOwned
	}
	selected.ActionApplications = all
	return r, selected, nil
}

// The fixed-size digest carries every earlier deletion tombstone forward when
// the next owner retires. It grants no ownership of any historical native UUID.
func discoveryRetiredRows(nc *cvk.NetworkController, excludeUID string) ([]cvk.NetworkControllerConfigurationActionApplication, error) {
	var rows []cvk.NetworkControllerConfigurationActionApplication
	seen := map[string]bool{}
	currentOwner := ""
	for _, o := range nc.Status.Configuration.Owners {
		if o.Section == "inventory" {
			currentOwner = o.ConfigUID
		}
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if !strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) || excludeUID != "" && r.Operation.ConfigUID == excludeUID {
			continue
		}
		// Imported foreign work is not retired history and is never adopted.
		// Its ordinary same-name custody checks remain unchanged.
		if r.Operation.ConfigUID != currentOwner && (r.Operation.Action != "Delete" || !strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix)) {
			continue
		}
		if !validOwnedDiscoveryReceipt(r) || r.Operation.Section != "inventory" || r.Operation.Action != "Delete" || !strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) || r.Operation.ControllerGeneration > nc.Generation || seen[r.Operation.TargetID] {
			return nil, errConfigurationOwned
		}
		seen[r.Operation.TargetID] = true
		rows = append(rows, r)
	}
	return rows, nil
}

func discoveryRetiredReceiptKeys(nc *cvk.NetworkController, ownerUID string) map[string]bool {
	if nc.Status.Configuration == nil {
		return nil
	}
	owners := 0
	for _, o := range nc.Status.Configuration.Owners {
		if configurationSectionsOverlap(o.Section, "inventory") {
			if o.Section != "inventory" || o.ConfigUID != ownerUID {
				return nil
			}
			owners++
		}
	}
	if owners != 1 {
		return nil
	}
	r, err := decodeDiscoveryRetirement(nc)
	if err != nil {
		return nil
	}
	exclude := ownerUID
	if r.Completion.ConfigUID == ownerUID {
		exclude = ""
	}
	rows, err := discoveryRetiredRows(nc, exclude)
	if err != nil || featureIdentityDigest(rows) != r.RetiredDigest {
		return nil
	}
	keys := map[string]bool{}
	for _, row := range rows {
		if row.Operation.ConfigUID != ownerUID {
			keys[row.Resource] = true
		}
	}
	return keys
}

func (e *ownedDiscoveryExecutor) bindRetiredHistory(nc *cvk.NetworkController, ownerUID string) {
	owned := false
	if nc.Status.Configuration != nil {
		for _, o := range nc.Status.Configuration.Owners {
			owned = owned || o.Section == "inventory" && o.ConfigUID == ownerUID
		}
	}
	if !owned {
		return
	}
	e.retired = discoveryRetiredReceiptKeys(nc, ownerUID)
	e.retiredInvalid = meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition) != nil && e.retired == nil
	if nc.Status.Configuration != nil {
		for _, r := range nc.Status.Configuration.ActionApplications {
			if strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) && r.Operation.ConfigUID != ownerUID && r.Operation.Action == "Delete" && strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) && !e.retired[r.Resource] {
				e.retiredInvalid = true
			}
		}
	}
}

func (a *adapter) discoveryRetiredConfigGone(ctx context.Context, r *discoveryOwnershipRetirement) error {
	var previous configapi.NetworkControllerConfig
	err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: a.key.Namespace, Name: r.ConfigName}, &previous)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(previous.UID) == r.Completion.ConfigUID {
		return errConfigurationOwned
	}
	// A same-name replacement has a different UID; it cannot resurrect the old
	// object or make its retained task/receipt identities transferable.
	return nil
}

// This changes Kubernetes ownership only. Normal planning and the existing
// preparation/start/submit CAS remain mandatory on a later reconcile.
func (a *adapter) handoffDiscoveryOwnership(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, error) {
	// Credential history has its own sealed custody gate. Ordinary handoff
	// must never reinterpret its receipts or no-submission witness.
	if c != nil && (len(c.Spec.SecretRefs) > 0 || discoveryCredentialHistoryOwner(nc, c)) {
		return a.handoffDiscoveryCredentialOwnership(ctx, nc, c, source)
	}
	if !discoveryDeletionEligible(nc, c) || !hasDiscoveryConfigFinalizer(c) || source == nil || !pureDiscoverySource(source.raw) {
		return false, nil
	}
	if _, err := parseInventoryIntent(source.raw); err != nil {
		return false, err
	}
	if nc.Status.Configuration == nil {
		return false, nil
	}
	foreign := false
	for _, o := range nc.Status.Configuration.Owners {
		if configurationSectionsOverlap(o.Section, "inventory") && (o.ConfigUID != string(c.UID) || o.ConfigName != c.Name) {
			foreign = true
		}
	}
	if !foreign {
		return false, nil
	}
	retired, selected, err := discoveryRetirementRecord(nc)
	if err != nil {
		return true, err
	}
	if err := a.discoveryRetiredConfigGone(ctx, retired); err != nil {
		return true, err
	}
	var baseline map[string]discoveryMetadata
	for i := range 2 {
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return true, err
		}
		for _, receipt := range selected.ActionApplications {
			for _, row := range actual {
				if row.ID == receipt.Operation.TargetID {
					return true, errConfigurationOwned
				}
			}
		}
		if i == 0 {
			baseline = actual
		} else if !reflect.DeepEqual(actual, baseline) {
			return true, errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return true, err
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return true, err
		}
		if fresh.UID != nc.UID || fresh.Generation != nc.Generation || fresh.ResourceVersion != nc.ResourceVersion || fresh.Spec.Paused || !fresh.DeletionTimestamp.IsZero() {
			return true, errConfigurationStale
		}
		if err := a.discoveryRetiredConfigGone(ctx, retired); err != nil {
			return true, err
		}
	}
	for i := range nc.Status.Configuration.Owners {
		o := &nc.Status.Configuration.Owners[i]
		if o.ConfigUID == retired.Completion.ConfigUID && o.ConfigName == retired.ConfigName && o.Section == "inventory" {
			*o = cvk.NetworkControllerSectionOwner{Section: "inventory", ConfigName: c.Name, ConfigUID: string(c.UID)}
		}
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, c); err != nil {
		return true, err
	}
	return true, a.statusWriter.Status().Update(ctx, nc)
}
