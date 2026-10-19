// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const siteRecreatePrefix = "site-private-recreate:v1:"
const siteRecreateHandoffPrefix = "site-private-recreate:v2:"
const siteHistoryPrefix = "site-history:v1:"

func siteRecreateSelected(resource string) bool {
	return strings.HasPrefix(resource, "site-private-recreate:")
}

func siteRecreationParts(resource string) ([]string, bool) {
	prefix, count := siteRecreatePrefix, 6
	if strings.HasPrefix(resource, siteRecreateHandoffPrefix) {
		prefix, count = siteRecreateHandoffPrefix, 7
	}
	if !strings.HasPrefix(resource, prefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, prefix), ":")
	if len(parts) != count || !configurationOperationID.MatchString(parts[4]) || !validTemplateComparisonDigest(parts[5]) || count == 7 && !validTemplateComparisonDigest(parts[6]) {
		return nil, false
	}
	if _, ok := sitePrivateCreateParts(sitePrivateCreatePrefix + strings.Join(parts[:4], ":")); !ok {
		return nil, false
	}
	return parts, true
}
func sitePrivateCreationParts(resource string) ([]string, bool) {
	if !siteRecreateSelected(resource) {
		return sitePrivateCreateParts(resource)
	}
	parts, ok := siteRecreationParts(resource)
	if !ok {
		return nil, false
	}
	return sitePrivateCreateParts(sitePrivateCreatePrefix + strings.Join(parts[:4], ":"))
}
func siteRecreationParent(resource string) (string, string, bool) {
	if !siteRecreateSelected(resource) {
		return "", "", false
	}
	if _, ok := sitePrivateCreationParts(resource); !ok {
		return "", "", false
	}
	parts, _ := siteRecreationParts(resource)
	return parts[4], parts[5], true
}

type siteHistoryCohort struct {
	Path, Kind, ID, Digest string // Path is a public identity hash, never a raw path.
	Creator, Retired       cvk.NetworkControllerConfigurationActionApplication
	Rows                   []cvk.NetworkControllerConfigurationActionApplication
}

func siteCohortDigest(rows []cvk.NetworkControllerConfigurationActionApplication) string {
	ordered := append([]cvk.NetworkControllerConfigurationActionApplication(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Resource < ordered[j].Resource })
	digests := make([]string, 0, len(ordered))
	for _, row := range ordered {
		digests = append(digests, siteReceiptDigest(row))
	}
	raw, _ := json.Marshal(digests)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func siteCurrentRetirementCohorts(state *cvk.NetworkControllerConfigurationStatus) (map[string]siteHistoryCohort, error) {
	out := map[string]siteHistoryCohort{}
	if state == nil {
		return out, nil
	}
	creators, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	applied, err := siteAppliedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	retired, err := siteRetiredRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	for _, row := range retired {
		parts := strings.Split(strings.TrimPrefix(row.Operation.Resource, siteRetirementPrefix), ":")
		var creator siteCreatorRecord
		for _, candidate := range creators {
			if siteReceiptDigest(candidate.receipt) == parts[4] {
				creator = candidate
			}
		}
		if creator.id == "" {
			return nil, errConfigurationOwned
		}
		rows := []cvk.NetworkControllerConfigurationActionApplication{creator.receipt}
		if latest, found := applied[parts[1]]; found {
			rows = append(rows, latest)
		}
		rows = append(rows, row)
		out[parts[1]] = siteHistoryCohort{Path: parts[1], Kind: parts[0], ID: creator.id, Digest: siteCohortDigest(rows), Creator: creator.receipt, Retired: row, Rows: rows}
	}
	return out, nil
}

// Reconstruct complete archived lifecycles using the same structural validators
// as live rows. Internal Current readers avoid recursively validating history;
// public readers additionally require every archive to be reached by a successor.
func siteHistoryCohorts(state *cvk.NetworkControllerConfigurationStatus) (map[string]siteHistoryCohort, error) {
	out := map[string]siteHistoryCohort{}
	if state == nil {
		return out, nil
	}
	counts := map[string]int{}
	for _, row := range state.ActionApplications {
		counts[row.Operation.ID]++
	}
	grouped := map[string][]cvk.NetworkControllerConfigurationActionApplication{}
	for _, row := range state.ActionApplications {
		if !strings.HasPrefix(row.Resource, "site-history:") {
			continue
		}
		if !strings.HasPrefix(row.Resource, siteHistoryPrefix) || len(row.Resource) > 512 || counts[row.Operation.ID] != 1 {
			return nil, errConfigurationOwned
		}
		digest, original, ok := strings.Cut(strings.TrimPrefix(row.Resource, siteHistoryPrefix), ":")
		if !ok || !validTemplateComparisonDigest(digest) || original == "" || strings.HasPrefix(original, "site-history:") {
			return nil, errConfigurationOwned
		}
		copy := *row.DeepCopy()
		copy.Resource = original
		grouped[digest] = append(grouped[digest], copy)
	}
	archiveIDs := map[string]bool{}
	for digest, rows := range grouped {
		local := &cvk.NetworkControllerConfigurationStatus{Owners: state.Owners, ActionApplications: rows}
		cohorts, err := siteCurrentRetirementCohorts(local)
		if err != nil || len(cohorts) != 1 {
			return nil, errConfigurationOwned
		}
		for _, cohort := range cohorts {
			if len(cohort.Rows) != len(rows) || siteCohortDigest(rows) != digest {
				return nil, errConfigurationOwned
			}
			if _, duplicate := out[cohort.Retired.Operation.ID]; duplicate {
				return nil, errConfigurationOwned
			}
			if archiveIDs[cohort.ID] {
				return nil, errConfigurationOwned
			}
			archiveIDs[cohort.ID] = true
			out[cohort.Retired.Operation.ID] = cohort
		}
	}
	current, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	visited := map[string]bool{}
	for _, record := range current {
		for _, cohort := range out {
			if record.id == cohort.ID {
				return nil, errConfigurationOwned
			}
			for _, row := range cohort.Rows {
				if record.receipt.Operation.TaskID == row.Operation.TaskID {
					return nil, errConfigurationOwned
				}
			}
		}
		creator := record.receipt
		seen := map[string]bool{}
		descendantTasks := map[string]bool{creator.Operation.TaskID: true}
		for siteRecreateSelected(creator.Operation.Resource) {
			parent, digest, ok := siteRecreationParent(creator.Operation.Resource)
			cohort, found := out[parent]
			if !ok || !found || seen[parent] || visited[parent] || digest != cohort.Digest || siteResourcePathDigest(creator.Operation.Resource) != cohort.Path || creator.Operation.ConfigName != cohort.Retired.Operation.ConfigName || creator.Operation.ConfigUID != cohort.Retired.Operation.ConfigUID || creator.Operation.ControllerGeneration < cohort.Retired.Operation.ControllerGeneration || !configurationInputAfter(&creator.Operation, &cohort.Retired.Operation) || creator.Operation.SubmittedAt == nil || creator.Operation.SubmittedAt.Before(cohort.Retired.VerifiedAt) {
				return nil, errConfigurationOwned
			}
			if _, err := siteRecreationParentProof(state, &creator.Operation, cohort, out, false); err != nil {
				return nil, err
			}
			parts, valid := sitePrivateCreationParts(creator.Operation.Resource)
			old, oldValid := sitePrivateCreationParts(cohort.Creator.Operation.Resource)
			if !valid || !oldValid || parts[0] != cohort.Kind || parts[3] != old[3] {
				return nil, errConfigurationOwned
			}
			for _, row := range cohort.Rows {
				if descendantTasks[row.Operation.TaskID] {
					return nil, errConfigurationOwned
				}
			}
			for _, row := range cohort.Rows {
				descendantTasks[row.Operation.TaskID] = true
			}
			seen[parent], visited[parent] = true, true
			creator = cohort.Creator
		}
	}
	if len(visited) != len(out) {
		return nil, errConfigurationOwned
	}
	return out, nil
}

func siteRecreationAuthority(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (siteHistoryCohort, error) {
	var empty siteHistoryCohort
	if !siteCreateSelected(op) || !siteSingularOperation(op) || !siteRecreateSelected(op.Resource) {
		return empty, errConfigurationTransition
	}
	archives, err := siteHistoryCohorts(state)
	if err != nil {
		return empty, err
	}
	if _, err := siteRetiredRecords(state); err != nil {
		return empty, err
	}
	cohorts, err := siteCurrentRetirementCohorts(state)
	if err != nil {
		return empty, err
	}
	parent, digest, ok := siteRecreationParent(op.Resource)
	cohort, found := cohorts[siteResourcePathDigest(op.Resource)]
	parts, valid := sitePrivateCreationParts(op.Resource)
	old, oldValid := sitePrivateCreationParts(cohort.Creator.Operation.Resource)
	if !ok || !found || !valid || !oldValid || parent != cohort.Retired.Operation.ID || digest != cohort.Digest || parts[0] != cohort.Kind || parts[3] != old[3] || op.ConfigName != cohort.Retired.Operation.ConfigName || op.ConfigUID != cohort.Retired.Operation.ConfigUID || op.ControllerGeneration < cohort.Retired.Operation.ControllerGeneration || !configurationInputAfter(op, &cohort.Retired.Operation) || op.SubmittedAt != nil && op.SubmittedAt.Before(cohort.Retired.VerifiedAt) {
		return empty, errConfigurationOwned
	}
	if _, err := siteRecreationParentProof(state, op, cohort, archives, true); err != nil {
		return empty, err
	}
	for _, row := range state.ActionApplications {
		if op.ID != "" && row.Operation.ID == op.ID || op.TaskID != "" && row.Operation.TaskID == op.TaskID {
			return empty, errConfigurationOwned
		}
	}
	return cohort, nil
}

// Stage archival only in a copy; publish with the new verified creator in one
// status CAS. Returning this incomplete copy is not permission to persist it.
func siteArchivePredecessor(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkControllerConfigurationStatus, error) {
	if state == nil || op == nil || !reflect.DeepEqual(state.Operation, op) || (op.Phase != "Submitting" && op.Phase != "Verifying") {
		return nil, errConfigurationTransition
	}
	if len(state.ActionApplications) >= 256 {
		return nil, errConfigurationBusy
	}
	cohort, err := siteRecreationAuthority(state, op)
	if err != nil {
		return nil, err
	}
	out := state.DeepCopy()
	count := 0
	for i := range out.ActionApplications {
		row := &out.ActionApplications[i]
		for _, original := range cohort.Rows {
			if reflect.DeepEqual(*row, original) {
				row.Resource = siteHistoryPrefix + cohort.Digest + ":" + row.Resource
				if len(row.Resource) > 512 {
					return nil, errConfigurationBusy
				}
				count++
				break
			}
		}
	}
	if count != len(cohort.Rows) {
		return nil, errConfigurationOwned
	}
	raw, err := json.Marshal(out.ActionApplications)
	if err != nil || len(raw) > 256<<10 {
		return nil, errConfigurationBusy
	}
	return out, nil
}

func siteHistoryTaskUnused(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || op == nil || op.TaskID == "" {
		return nil
	}
	for _, row := range state.ActionApplications {
		if strings.HasPrefix(row.Resource, "site-history:") && row.Operation.TaskID == op.TaskID {
			return errConfigurationOwned
		}
	}
	return nil
}
