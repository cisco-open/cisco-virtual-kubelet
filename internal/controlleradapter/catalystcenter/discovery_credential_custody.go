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
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const discoveryCredentialCustodyVersion = "credential-discovery-custody/v1"

// An audit of one metadata-only ownership CAS, embedded in the existing
// retirement condition. It is not a native permit or a replacement source.
type discoveryCredentialCustody struct {
	Version, ControllerUID, ConfigUID, ConfigName                   string
	ControllerGeneration, ConfigGeneration                          int64
	SpecDigest, SourceUID, SourceVersion, ModelDigest, InputsDigest string
	PreviousRetirementDigest                                        string
}

// Private binding/readback prerequisite. Normal execution must independently
// validate current sources, native absence and each operation's own admission.
// This currently requires the exact initial successor revision; it does not
// silently grant authority for later source/spec changes or another owner.
func discoveryCredentialCustodyRecord(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (*discoveryOwnershipRetirement, *discoveryCredentialCohort, error) {
	if source == nil {
		return nil, nil, errConfigurationOwned
	}
	return discoveryCredentialCustodyBinding(nc, c, string(source.uid), source.version, featureIdentityDigest(source.raw))
}

func discoveryCredentialCustodyBinding(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, sourceUID, sourceVersion, modelDigest string) (*discoveryOwnershipRetirement, *discoveryCredentialCohort, error) {
	if c == nil || !c.DeletionTimestamp.IsZero() {
		return nil, nil, errConfigurationOwned
	}
	record, cohort, err := discoveryCredentialCustodyRoot(nc, c)
	if err != nil {
		return nil, nil, err
	}
	link := record.CredentialCustody
	if link.ConfigGeneration != c.Generation || link.SpecDigest != featureIdentityDigest(c.Spec) || link.SourceUID != sourceUID || link.SourceVersion != sourceVersion || link.ModelDigest != modelDigest {
		return nil, nil, errConfigurationOwned
	}
	return record, cohort, nil
}

// Immutable provenance only; current source/scope and native admission are
// independent. This root never substitutes a historical spec or source body.
func discoveryCredentialCustodyRoot(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*discoveryOwnershipRetirement, *discoveryCredentialCohort, error) {
	if nc == nil || c == nil || nc.UID == "" || nc.Generation < 1 || c.UID == "" || c.Generation < 1 || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || !discoveryCredentialOwnedDeletionSpecShape(nc, c) || !hasDiscoveryConfigFinalizer(c) || c.Namespace != nc.Namespace || c.Spec.ControllerRef.Name != nc.Name || controlleradapter.ValidateConfigMetadataContract(nc, c) != nil || nc.Status.Configuration == nil {
		return nil, nil, errConfigurationOwned
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DiscoveryConfigClosed" || cond.ObservedGeneration != nc.Generation {
		return nil, nil, errConfigurationOwned
	}
	var record discoveryOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &record) != nil {
		return nil, nil, errConfigurationOwned
	}
	raw, _ := json.Marshal(record)
	link := record.CredentialCustody
	if string(raw) != cond.Message || link == nil || link.Version != discoveryCredentialCustodyVersion || link.ControllerUID != string(nc.UID) || link.ControllerGeneration != nc.Generation || link.ConfigUID != string(c.UID) || link.ConfigName != c.Name || link.ConfigGeneration < 1 || link.ConfigGeneration > c.Generation || link.ConfigUID == record.Completion.ConfigUID || !lowerDiscoveryDigest(link.SpecDigest) || !lowerDiscoveryDigest(link.ModelDigest) || !lowerDiscoveryDigest(link.InputsDigest) || (link.SourceUID == "") != (link.SourceVersion == "") {
		return nil, nil, errConfigurationOwned
	}
	for _, row := range nc.Status.Configuration.ActionApplications {
		if row.Operation.ConfigUID == string(c.UID) && (row.Operation.ConfigName != c.Name || row.Operation.ConfigGeneration < link.ConfigGeneration || row.Operation.ConfigGeneration > c.Generation || row.Operation.ControllerGeneration != nc.Generation) {
			return nil, nil, errConfigurationOwned
		}
	}
	if op := nc.Status.Configuration.Operation; op != nil && (op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || op.ConfigGeneration < link.ConfigGeneration || op.ConfigGeneration > c.Generation || op.ControllerGeneration != nc.Generation) {
		return nil, nil, errConfigurationOwned
	}
	cohort, err := discoveryCredentialHistoryCohort(nc.Status.Configuration, string(c.UID))
	if err != nil {
		return nil, nil, err
	}
	if err := cohort.validateArchiveSeal(record.CredentialHistoryDigest); err != nil {
		return nil, nil, err
	}
	if record.UnsubmittedOperation != nil {
		if !strings.HasPrefix(record.Completion.CredentialDigest, discoveryCredentialUnsubmittedPrefix) {
			return nil, nil, errConfigurationOwned
		}
		witness := record.UnsubmittedOperation.ID
		if nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.ID == witness {
			return nil, nil, errConfigurationOwned
		}
		for _, row := range nc.Status.Configuration.ActionApplications {
			if row.Operation.ID == witness {
				return nil, nil, errConfigurationOwned
			}
		}
		for _, row := range nc.Status.Configuration.CredentialApplications {
			if row.OperationID == witness {
				return nil, nil, errConfigurationOwned
			}
		}
	}
	if cohort.archived[record.Completion.ConfigUID] == nil && record.UnsubmittedOperation == nil {
		return nil, nil, errConfigurationOwned
	}
	original := record
	original.CredentialCustody = nil
	if featureIdentityDigest(&original) != link.PreviousRetirementDigest {
		return nil, nil, errConfigurationOwned
	}
	// Reconstruct only the immutable predecessor view for its existing strict
	// proof validator. This never relabels any retained operation or receipt.
	previous := nc.DeepCopy()
	state := previous.Status.Configuration
	state.Operation = nil
	for i := range state.Owners {
		if state.Owners[i].Section == "inventory" {
			state.Owners[i] = cvk.NetworkControllerSectionOwner{Section: "inventory", ConfigName: record.ConfigName, ConfigUID: record.Completion.ConfigUID}
		}
	}
	actions := state.ActionApplications[:0]
	for _, row := range state.ActionApplications {
		if row.Operation.ConfigUID == string(c.UID) {
			if !discoveryCredentialCohortAction(row) {
				return nil, nil, errConfigurationOwned
			}
			continue
		}
		actions = append(actions, row)
	}
	state.ActionApplications = actions
	applications := state.CredentialApplications[:0]
	for _, row := range state.CredentialApplications {
		if row.ConfigUID == string(c.UID) {
			if !discoveryCredentialCohortApplication(row) {
				return nil, nil, errConfigurationOwned
			}
			continue
		}
		applications = append(applications, row)
	}
	state.CredentialApplications = applications
	oldRaw, _ := json.Marshal(original)
	meta.FindStatusCondition(previous.Status.Conditions, discoveryRetirementCondition).Message = string(oldRaw)
	if _, _, err := discoveryCredentialRetiredHandoffRecord(previous); err != nil {
		return nil, nil, err
	}
	return &record, cohort, nil
}

// Publish only after repeating the complete read-only observation. The original
// reservation and custody record change in one status CAS; receipt history and
// the successor config remain untouched. Public orchestration adds no native permit.
func (a *adapter) publishDiscoveryCredentialCustody(ctx context.Context, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, observed *discoveryCredentialHandoffObservation) error {
	if observed == nil {
		return errConfigurationTransition
	}
	if source == nil || source.secrets == nil {
		return errConfigurationSecret
	}
	secrets := source.secrets
	if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
		return err
	}
	fresh, err := a.observeDiscoveryCredentialOwnershipHandoff(ctx, c, source)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, observed) {
		return errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.ResourceVersion != observed.ControllerVersion || featureIdentityDigest(nc.Status.Configuration) != observed.EvidenceDigest {
		return errConfigurationStale
	}
	retired, _, err := discoveryCredentialRetiredHandoffRecord(nc)
	if err != nil {
		return err
	}
	if retired.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+discoveryCredentialAuditDigest(nc.Status.Configuration, "") || featureIdentityDigest(retired) != observed.RetirementDigest {
		return errConfigurationOwned
	}
	retired.CredentialCustody = &discoveryCredentialCustody{Version: discoveryCredentialCustodyVersion, ControllerUID: string(nc.UID), ControllerGeneration: nc.Generation, ConfigUID: string(c.UID), ConfigName: c.Name, ConfigGeneration: c.Generation, SpecDigest: observed.SpecDigest, SourceUID: observed.SourceUID, SourceVersion: observed.SourceVersion, ModelDigest: observed.ModelDigest, InputsDigest: observed.InputsDigest, PreviousRetirementDigest: observed.RetirementDigest}
	for i := range nc.Status.Configuration.Owners {
		o := &nc.Status.Configuration.Owners[i]
		if o.Section == "inventory" && o.ConfigUID == retired.Completion.ConfigUID && o.ConfigName == retired.ConfigName {
			*o = cvk.NetworkControllerSectionOwner{Section: "inventory", ConfigName: c.Name, ConfigUID: string(c.UID)}
		}
	}
	raw, _ := json.Marshal(retired)
	if len(raw) > 32768 {
		return errConfigurationTransition
	}
	meta.SetStatusCondition(&nc.Status.Conditions, condition(discoveryRetirementCondition, true, "DiscoveryConfigClosed", string(raw), nc.Generation))
	if _, _, err := discoveryCredentialCustodyRecord(nc, c, source); err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
		return err
	}
	if err := secrets.unchanged(nc, c); err != nil {
		return err
	}
	if err := a.discoveryRetiredConfigGone(ctx, retired); err != nil {
		return err
	}
	return a.statusWriter.Status().Update(ctx, nc)
}
