// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Retirement releases a native child dependency, not the child's custody or
// producer history. Only whole, verified descendant cohorts may be excluded
// from the parent's retained-effect fence. Every other effect remains blocking.
type siteRetiredDependents struct {
	rows, ids, paths map[string]bool
}

func siteRetiredDependentCohorts(state *cvk.NetworkControllerConfigurationStatus, parent *cvk.NetworkControllerConfigurationOperation, kind, keyID string) (*siteRetiredDependents, error) {
	out := &siteRetiredDependents{rows: map[string]bool{}, ids: map[string]bool{}, paths: map[string]bool{}}
	if state == nil || parent == nil || !canonicalTemplateUUID(parent.TargetID) || parent.ConfigName == "" || parent.ConfigUID == "" || parent.ConfigGeneration < 1 || parent.ControllerGeneration < 1 || !validTemplateComparisonDigest(keyID) || kind != "area" && kind != "building" && kind != "floor" {
		return nil, errConfigurationOwned
	}
	creators, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	applied, err := siteAppliedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	// Validate each candidate in an isolated cohort. Its native ID cannot equal
	// its parent ID, so validating its own retirement has no child dependency
	// and never recurses through the surrounding parent/child graph.
	frontier := map[string]string{parent.TargetID: kind}
	authority := map[string]*cvk.NetworkControllerConfigurationOperation{parent.TargetID: parent}
	accept := func(original, canonical []cvk.NetworkControllerConfigurationActionApplication, parentID, parentKind string) error {
		local := &cvk.NetworkControllerConfigurationStatus{Owners: state.Owners, ActionApplications: canonical}
		cohorts, err := siteCurrentRetirementCohorts(local)
		if err != nil || len(cohorts) != 1 {
			return errConfigurationOwned
		}
		for _, c := range cohorts {
			base, private := sitePrivateCreationParts(c.Creator.Operation.Resource)
			r := c.Retired
			link := authority[parentID]
			if len(c.Rows) != len(canonical) || !private || base[3] != keyID || c.Creator.Operation.ParentID != parentID || !validSiteParent(c.Kind, parentKind) || c.ID == parent.TargetID || r.Operation.ConfigName != parent.ConfigName || r.Operation.ConfigUID != parent.ConfigUID || r.Operation.ConfigGeneration > link.ConfigGeneration || r.Operation.ControllerGeneration > link.ControllerGeneration || link.SubmittedAt != nil && link.SubmittedAt.Before(r.VerifiedAt) {
				return errConfigurationOwned
			}
			if _, duplicate := frontier[c.ID]; duplicate {
				return errConfigurationOwned
			}
			for _, row := range c.Rows {
				if parent.ID != "" && parent.ID == row.Operation.ID || parent.TaskID != "" && parent.TaskID == row.Operation.TaskID || link.ID != "" && link.ID == row.Operation.ID || link.TaskID != "" && link.TaskID == row.Operation.TaskID {
					return errConfigurationOwned
				}
			}
			for _, row := range original {
				out.rows[siteReceiptDigest(row)] = true
			}
			out.ids[c.ID], out.paths[c.Path] = true, true
			frontier[c.ID] = c.Kind
			authority[c.ID] = r.Operation.DeepCopy()
		}
		return nil
	}
	type candidate struct {
		original, canonical []cvk.NetworkControllerConfigurationActionApplication
		digest              string
	}
	candidates := []candidate{}
	for _, creator := range creators {
		rows := []cvk.NetworkControllerConfigurationActionApplication{creator.receipt}
		path := siteResourcePathDigest(creator.receipt.Operation.Resource)
		if latest, found := applied[path]; found {
			rows = append(rows, latest)
		}
		retirements := []cvk.NetworkControllerConfigurationActionApplication{}
		for _, row := range state.ActionApplications {
			if !strings.HasPrefix(row.Resource, "site-history:") && strings.HasPrefix(row.Resource, "site-retired:") && row.Operation.TargetID == creator.id {
				retirements = append(retirements, row)
			}
		}
		rows = append(rows, retirements...)
		candidates = append(candidates, candidate{original: rows, canonical: rows})
	}
	// Older lifecycles can retain references to this same parent UUID too.
	// Unwrap and validate complete archive groups; never exempt an isolated row.
	groups := map[string][]cvk.NetworkControllerConfigurationActionApplication{}
	for _, row := range state.ActionApplications {
		if !strings.HasPrefix(row.Resource, "site-history:") {
			continue
		}
		if !strings.HasPrefix(row.Resource, siteHistoryPrefix) {
			return nil, errConfigurationOwned
		}
		digest, _, ok := strings.Cut(strings.TrimPrefix(row.Resource, siteHistoryPrefix), ":")
		if !ok || !validTemplateComparisonDigest(digest) {
			return nil, errConfigurationOwned
		}
		groups[digest] = append(groups[digest], row)
	}
	for digest, rows := range groups {
		canonical := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(rows))
		for _, row := range rows {
			copy := *row.DeepCopy()
			copy.Resource = strings.TrimPrefix(copy.Resource, siteHistoryPrefix+digest+":")
			canonical = append(canonical, copy)
		}
		candidates = append(candidates, candidate{original: rows, canonical: canonical, digest: digest})
	}
	// Walk the bounded retained graph, rather than trusting a retired direct
	// child while overlooking a live or reappeared grandchild below it.
	processed := map[int]bool{}
	for {
		changed := false
		for i, c := range candidates {
			if processed[i] {
				continue
			}
			for _, row := range c.canonical {
				parentKind, reachable := frontier[row.Operation.ParentID]
				if !reachable {
					continue
				}
				if c.digest != "" && siteCohortDigest(c.canonical) != c.digest {
					return nil, errConfigurationOwned
				}
				if err := accept(c.original, c.canonical, row.Operation.ParentID, parentKind); err != nil {
					return nil, err
				}
				processed[i], changed = true, true
				break
			}
		}
		if !changed {
			break
		}
	}
	for _, row := range state.ActionApplications {
		_, underParent := frontier[row.Operation.ParentID]
		if (underParent || out.ids[row.Operation.TargetID]) && !out.rows[siteReceiptDigest(row)] {
			return nil, errConfigurationOwned
		}
	}
	for _, row := range state.CredentialApplications {
		if out.ids[row.TargetID] {
			return nil, errConfigurationOwned
		}
	}
	return out, nil
}

func (d *siteRetiredDependents) checkAbsent(actual []Site) error {
	for _, s := range actual {
		if d.ids[s.ID] || d.paths[sitePathDigest(s.NameHierarchy)] {
			return errConfigurationOwned
		}
	}
	return nil
}
