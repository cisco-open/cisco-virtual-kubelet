// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"slices"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Derived validation only, never a reservation or submission capability. The
// archive digest must be bound to qualified retirement/handoff evidence before
// a public executor may consume it. The existing single-owner parser stays
// strict; no original receipt, UID, version, owner or native ID is relabeled.
type discoveryCredentialCohort struct {
	current       *discoveryCredentialLineage
	archived      map[string]*discoveryCredentialLineage
	archiveDigest string
	allDigest     string
}

func discoveryCredentialCohortAction(row cvk.NetworkControllerConfigurationActionApplication) bool {
	return strings.HasPrefix(row.Resource, "discovery-credential-") || strings.HasPrefix(row.Operation.Resource, "discovery-credential-") || discoveryFinalizeSelected(row.Operation.Resource) && strings.Contains(row.Operation.Resource, "discovery-credential-")
}
func discoveryCredentialCohortApplication(row cvk.NetworkControllerConfigurationCredentialApplication) bool {
	return strings.HasPrefix(row.Resource, "discovery-credential-")
}

// Validate the entire shared ID namespace before making per-owner projections.
// Each archived owner must retain a complete, closed typed lineage. Synthetic
// Owners below are local parser inputs only; they never change the reservation
// or assert provenance for imported records.
func discoveryCredentialHistoryCohort(state *cvk.NetworkControllerConfigurationStatus, owner string) (*discoveryCredentialCohort, error) {
	if state == nil || owner == "" {
		return nil, errConfigurationOwned
	}
	ownerName := ""
	ownerCount := 0
	for _, o := range state.Owners {
		if !configurationSectionsOverlap(o.Section, "inventory") {
			continue
		}
		if o.Section != "inventory" || o.ConfigUID != owner || o.ConfigName == "" {
			return nil, errConfigurationOwned
		}
		ownerName = o.ConfigName
		ownerCount++
	}
	if ownerCount != 1 || state.Operation != nil && state.Operation.ConfigUID != owner {
		return nil, errConfigurationOwned
	}
	names := map[string]string{owner: ownerName}
	ids := map[string]int{}
	for _, row := range state.ActionApplications {
		ids[row.Operation.ID]++
	}
	for _, row := range state.ActionApplications {
		if !discoveryCredentialCohortAction(row) {
			continue
		}
		op := row.Operation
		if op.ConfigUID == "" || op.ConfigName == "" || ids[op.ID] != 1 || !configurationOperationID.MatchString(op.ID) || !(discoveryCredentialReceiptSelected(row.Resource) && discoveryCredentialInputSelected(op.Resource) || discoveryCredentialRetiredSelected(row.Resource) && discoveryCredentialRetirementSelected(op.Resource)) {
			return nil, errConfigurationOwned
		}
		if name, seen := names[op.ConfigUID]; seen && name != op.ConfigName {
			return nil, errConfigurationOwned
		}
		names[op.ConfigUID] = op.ConfigName
	}
	for _, app := range state.CredentialApplications {
		if !discoveryCredentialCohortApplication(app) {
			continue
		}
		if !discoveryCredentialInputSelected(app.Resource) || app.ConfigUID == "" || names[app.ConfigUID] == "" {
			return nil, errConfigurationOwned
		}
	}
	cohort := &discoveryCredentialCohort{archived: map[string]*discoveryCredentialLineage{}}
	nativeIDs := map[string]bool{}
	for uid, name := range names {
		local := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: name, ConfigUID: uid}}}
		if uid == owner {
			local.Operation = state.Operation.DeepCopy()
		}
		for _, row := range state.ActionApplications {
			typed := discoveryCredentialCohortAction(row)
			if !typed || row.Operation.ConfigUID == uid {
				local.ActionApplications = append(local.ActionApplications, *row.DeepCopy())
			}
		}
		for _, app := range state.CredentialApplications {
			typed := discoveryCredentialCohortApplication(app)
			if !typed || app.ConfigUID == uid {
				local.CredentialApplications = append(local.CredentialApplications, *app.DeepCopy())
			}
		}
		for _, row := range state.GroupCancellations {
			if row.Operation.ConfigUID == uid {
				return nil, errConfigurationBusy
			}
		}
		history, err := discoveryCredentialLineages(local, uid)
		if err != nil {
			return nil, err
		}
		for _, creator := range history.creators {
			if nativeIDs[creator.identity.id] {
				return nil, errConfigurationOwned
			}
			nativeIDs[creator.identity.id] = true
		}
		if uid == owner {
			cohort.current = history
			continue
		}
		if len(history.latest) == 0 {
			return nil, errConfigurationOwned
		}
		for _, creator := range history.latest {
			if history.retired[creator.receipt.Operation.ID] == nil {
				return nil, errConfigurationOwned
			}
		}
		cohort.archived[uid] = history
	}
	// Old delivered floors apply to every active-owner incarnation, not just a
	// proposed next request. A locally valid root cannot reset another UID's floor.
	for _, creator := range cohort.current.creators {
		if err := cohort.archivedVersions(creator.identity.name, creator.application.Versions); err != nil {
			return nil, err
		}
	}
	cohort.archiveDigest = discoveryCredentialAuditDigest(state, owner)
	cohort.allDigest = discoveryCredentialAuditDigest(state, "")
	return cohort, nil
}

func (c *discoveryCredentialCohort) archivedVersions(name string, versions []cvk.NetworkControllerConfigurationCredentialVersion) error {
	if c == nil || c.current == nil {
		return errConfigurationOwned
	}
	for _, history := range c.archived {
		if err := discoveryCredentialSuccessorVersions(history, name, versions); err != nil {
			return err
		}
	}
	return nil
}
func (c *discoveryCredentialCohort) transition(name string, versions []cvk.NetworkControllerConfigurationCredentialVersion) error {
	if err := c.archivedVersions(name, versions); err != nil {
		return err
	}
	return c.current.transition(name, versions)
}
func (c *discoveryCredentialCohort) absentRetired(actual map[string]discoveryMetadata) error {
	if c == nil || c.current == nil {
		return errConfigurationOwned
	}
	if err := c.current.absentRetired(actual); err != nil {
		return err
	}
	for _, history := range c.archived {
		if err := history.absentRetired(actual); err != nil {
			return err
		}
	}
	return nil
}

// Digest verification only. A future custody consumer must also bind the
// original controller/config proof, reservation, source and ownership CAS.
// Legacy absence or an unknown version never authorizes archived execution.
func (c *discoveryCredentialCohort) validateArchiveSeal(seal string) error {
	if c == nil || !lowerDiscoveryDigest(c.archiveDigest) || seal != discoveryCredentialHistorySealPrefix+c.archiveDigest {
		return errConfigurationOwned
	}
	return nil
}

const discoveryCredentialHistorySealPrefix = "credential-discovery-history:v1:"

// Canonical digest input excludes reservation metadata and unrelated families.
// Sorting copies tolerates map-list order changes without rewriting the ledger.
func discoveryCredentialAuditDigest(state *cvk.NetworkControllerConfigurationStatus, exclude string) string {
	retained := &cvk.NetworkControllerConfigurationStatus{}
	for _, row := range state.ActionApplications {
		if discoveryCredentialCohortAction(row) && (exclude == "" || row.Operation.ConfigUID != exclude) {
			retained.ActionApplications = append(retained.ActionApplications, *row.DeepCopy())
		}
	}
	for _, row := range state.CredentialApplications {
		if discoveryCredentialCohortApplication(row) && (exclude == "" || row.ConfigUID != exclude) {
			retained.CredentialApplications = append(retained.CredentialApplications, *row.DeepCopy())
		}
	}
	slices.SortFunc(retained.ActionApplications, func(a, b cvk.NetworkControllerConfigurationActionApplication) int {
		if n := strings.Compare(a.Operation.ConfigUID, b.Operation.ConfigUID); n != 0 {
			return n
		}
		return strings.Compare(a.Operation.ID, b.Operation.ID)
	})
	slices.SortFunc(retained.CredentialApplications, func(a, b cvk.NetworkControllerConfigurationCredentialApplication) int {
		if n := strings.Compare(a.ConfigUID, b.ConfigUID); n != 0 {
			return n
		}
		return strings.Compare(a.OperationID, b.OperationID)
	})
	return featureIdentityDigest(retained)
}
