// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const wirelessTagRecreatePrefix = "tag-proof:v5:recreate:"
const wirelessTagLineagePrefix = "tag-proof:v5:"
const wirelessTagRetiredPrefix = "wireless-tag-retired:v1:"

func wirelessTagRecreateSelected(resource string) bool {
	return strings.HasPrefix(resource, "wireless-site-tags/"+wirelessTagRecreatePrefix) || strings.HasPrefix(resource, "wireless-policy-tags/"+wirelessTagRecreatePrefix)
}

func wirelessTagLineageSelected(resource string) bool {
	return strings.HasPrefix(resource, "wireless-site-tags/"+wirelessTagLineagePrefix) || strings.HasPrefix(resource, "wireless-policy-tags/"+wirelessTagLineagePrefix)
}

func wirelessTagRecreateParent(resource string) (string, bool) {
	if !wirelessTagRecreateSelected(resource) {
		return "", false
	}
	return wirelessTagLineageDigest(resource)
}

func wirelessTagLineageMode(resource string) string {
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		if local, ok := strings.CutPrefix(resource, ns+wirelessTagLineagePrefix); ok {
			mode, _, found := strings.Cut(local, ":")
			if found && (mode == "recreate" || mode == "write" || mode == "members" || mode == "delete") {
				return mode
			}
		}
	}
	return ""
}

func wirelessTagLineageDigest(resource string) (string, bool) {
	if wirelessTagLineageMode(resource) == "" {
		return "", false
	}
	if _, _, _, ok := wirelessTagPrivateParts(resource); !ok {
		return "", false
	}
	p := strings.Split(resource, ":")
	return p[len(p)-4], true
}

func wirelessTagLineageResource(resource, mode, digest string) (string, error) {
	if _, _, _, ok := wirelessTagPrivateParts(resource); !ok || !profileSiteDigestValid(digest) {
		return "", errConfigurationOwned
	}
	baseMode := mode
	if mode == "recreate" {
		baseMode = "write"
	}
	if baseMode != "write" && baseMode != "members" && baseMode != "delete" {
		return "", errConfigurationOwned
	}
	ns := strings.SplitN(resource, "/", 2)[0] + "/"
	local, ok := strings.CutPrefix(resource, ns+wirelessTagBoundPrefix+baseMode+":")
	if !ok {
		return "", errConfigurationOwned
	}
	p := strings.Split(local, ":")
	at := len(p) - 3
	parts := append(append(append([]string{}, p[:at]...), digest), p[at:]...)
	out := ns + wirelessTagLineagePrefix + mode + ":" + strings.Join(parts, ":")
	if len(out) > 512 {
		return "", errConfigurationOwned
	}
	return out, nil
}

// The private MAC authenticates lineage as well as native state. A public
// envelope/seal rewrite cannot reinterpret a recreated incarnation as v4.
func wirelessTagOperationBaseline(resource string, baseline any) (any, error) {
	if !wirelessTagLineageSelected(resource) {
		return baseline, nil
	}
	digest, ok := wirelessTagLineageDigest(resource)
	if !ok || baseline == nil {
		return nil, errConfigurationOwned
	}
	return map[string]any{"version": 5, "incarnation": digest, "native": baseline}, nil
}

func wirelessTagRecreationResource(resource string, cohort wirelessTagRecreationCohort) (string, error) {
	_, keyID, _, ok := wirelessTagPrivateParts(resource)
	if !ok || !strings.HasPrefix(resource, cohort.Namespace+wirelessTagBoundPrefix+"write:") || !profileSiteDigestValid(cohort.Digest) || cohort.ComparisonKeyID != keyID {
		return "", errConfigurationOwned
	}
	effects := []string{}
	for _, key := range cohort.Keys {
		effects = append(effects, key+":")
	}
	group := wirelessTagGroupKey(effects)
	if len(effects) == 0 || group != wirelessTagProducedGroup(resource) {
		return "", errConfigurationOwned
	}
	return wirelessTagLineageResource(resource, "recreate", cohort.Digest)
}

// A recreation must retire one complete original producer set, not just the
// latest row of one member. Rows are independent copies of the exact retained
// history; their digest can bind a later atomic archive/publication operation.
// This structural candidate is NOT dispatch authority: native absence, private
// tombstone MACs, current source/key and journal CAS still require live proof.
type wirelessTagRecreationCohort struct {
	Namespace, ParentID, ComparisonKeyID, Digest string
	Keys                                         []string
	NativeIDs                                    map[string]string
	Rows                                         []cvk.NetworkControllerConfigurationActionApplication
}

// Reuse the deletion protocol's native graph/scope and private MAC comparison.
// In particular, absence alone is insufficient after an RF/SSID replacement or
// a same-name/old-UUID resurrection. The journal/key proof brackets both reads.
// This does not reserve a write, retire rows or grant a submission permit.
func (e *wirelessTagPruneExecutor) recreationAbsent(ctx context.Context, cohort wirelessTagRecreationCohort) error {
	if e == nil || e.comparison == nil || e.proof == nil || cohort.ComparisonKeyID != e.comparison.keyID || len(cohort.Keys) == 0 || len(cohort.NativeIDs) != len(cohort.Keys) {
		return errConfigurationOwned
	}
	for range 2 {
		if err := e.proof(ctx, e.predecessor); err != nil {
			return err
		}
		for _, key := range cohort.Keys {
			old, exists := e.latest[cohort.Namespace+key]
			ns, logical, ids, valid := wirelessTagPruneParts(old.row.Operation.Resource)
			if !exists || !valid || ns != cohort.Namespace || logical != key || old.row.Operation.Action != "Delete" || wirelessTagBindingMode(old.row.Operation.Resource) != "delete" || old.row.Operation.ParentID != cohort.ParentID || old.id != cohort.NativeIDs[key] {
				return errConfigurationOwned
			}
			lineage, _ := wirelessTagLineageDigest(old.row.Operation.Resource)
			row, err := e.scopeLineage(ctx, ns, key, cohort.ParentID, old.id, ids, false, true, lineage)
			if err != nil {
				return err
			}
			if row.Actual != nil || wirelessTagCanonical(row.Resource) != wirelessTagCanonical(old.row.Operation.Resource) || row.Effect != key+":"+old.id+":"+old.mac {
				return errConfigurationOwned
			}
		}
	}
	return e.proof(ctx, e.predecessor)
}

func wirelessTagRecreationCandidate(state *cvk.NetworkControllerConfigurationStatus, ns string, keys []string, next *cvk.NetworkControllerConfigurationOperation) (wirelessTagRecreationCohort, error) {
	rows, err := wirelessTagRecords(state)
	if err != nil {
		return wirelessTagRecreationCohort{}, err
	}
	return wirelessTagRecreationCandidateRecords(rows, ns, keys, next)
}

func wirelessTagRecreationCandidateRecords(rows map[string]cvk.NetworkControllerConfigurationActionApplication, ns string, keys []string, next *cvk.NetworkControllerConfigurationOperation) (wirelessTagRecreationCohort, error) {
	out := wirelessTagRecreationCohort{Namespace: ns, Keys: append([]string{}, keys...), NativeIDs: map[string]string{}}
	if ns != "wireless-site-tags/" && ns != "wireless-policy-tags/" || len(keys) == 0 || len(keys) > 256 || next == nil || next.Section != "network_profiles" || next.Action != "Create" || next.TargetID != "" || !validAPIID(next.ParentID) {
		return out, errConfigurationOwned
	}
	sort.Strings(out.Keys)
	wanted := map[string]bool{}
	for _, key := range out.Keys {
		if !profileSiteDigestValid(key) || wanted[key] {
			return out, errConfigurationOwned
		}
		wanted[key] = true
	}
	latest, err := wirelessTagLatest(rows)
	if err != nil {
		return out, err
	}
	creators := 0
	for _, row := range rows {
		canonical, keyID, _, _ := wirelessTagPrivateParts(row.Operation.Resource)
		if !strings.HasPrefix(canonical, ns) {
			continue
		}
		memberKeys := []string{}
		intersects := false
		for _, effect := range row.EffectReceipts {
			key := strings.Split(effect, ":")[0]
			memberKeys = append(memberKeys, key)
			intersects = intersects || wanted[key]
		}
		if !intersects {
			continue
		}
		for _, key := range memberKeys {
			if !wanted[key] {
				return out, errConfigurationOwned
			}
		}
		op := &row.Operation
		if op.ParentID != next.ParentID || op.ConfigUID != next.ConfigUID || op.ConfigName != next.ConfigName || next.ConfigGeneration < op.ConfigGeneration || next.ControllerGeneration < op.ControllerGeneration {
			return out, errConfigurationOwned
		}
		if next.TaskID != "" && next.TaskID == op.TaskID {
			return out, errConfigurationOwned
		}
		if out.ComparisonKeyID != "" && out.ComparisonKeyID != keyID {
			return out, errConfigurationSecret
		}
		out.ComparisonKeyID = keyID
		if op.Action == "Create" {
			sort.Strings(memberKeys)
			if !reflect.DeepEqual(memberKeys, out.Keys) {
				return out, errConfigurationOwned
			}
			creators++
		}
		out.Rows = append(out.Rows, *row.DeepCopy())
	}
	if creators != 1 {
		return out, errConfigurationOwned
	}
	for _, key := range out.Keys {
		old, exists := latest[ns+key]
		if !exists || old.row.Operation.Action != "Delete" || wirelessTagBindingMode(old.row.Operation.Resource) != "delete" || !configurationInputAfter(next, &old.row.Operation) || next.SubmittedAt != nil && next.SubmittedAt.Before(old.row.VerifiedAt) {
			return out, errConfigurationOwned
		}
		out.NativeIDs[key] = old.id
	}
	out.ParentID = next.ParentID
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].Resource < out.Rows[j].Resource })
	out.Digest, err = wirelessTagDigest(out.Rows)
	return out, err
}

// Archived original rows keep their original seals and private effects. A new
// creator must name their exact digest; walking every reachable predecessor
// validates complete producer sets, chronology and owner/key continuity. No
// archive can be orphaned, shared by two successors or silently truncated.
func wirelessTagRetiredCohorts(state *cvk.NetworkControllerConfigurationStatus, current map[string]cvk.NetworkControllerConfigurationActionApplication) (map[string]wirelessTagRecreationCohort, error) {
	out := map[string]wirelessTagRecreationCohort{}
	if state == nil {
		return out, nil
	}
	groups := map[string][]cvk.NetworkControllerConfigurationActionApplication{}
	nonces := map[string]bool{}
	for _, row := range state.ActionApplications {
		archived := strings.HasPrefix(row.Resource, wirelessTagRetiredPrefix)
		if !archived && !wirelessTagPrivateSelected(row.Operation.Resource) {
			continue
		}
		if wirelessTagRecreateSelected(row.Operation.Resource) {
			for _, other := range state.ActionApplications {
				if other.Operation.ID != row.Operation.ID && other.Operation.TaskID == row.Operation.TaskID {
					return nil, errConfigurationOwned
				}
			}
		}
		if nonces[row.Operation.ID] {
			return nil, errConfigurationOwned
		}
		nonces[row.Operation.ID] = true
		if !archived {
			continue
		}
		digest, original, ok := strings.Cut(strings.TrimPrefix(row.Resource, wirelessTagRetiredPrefix), ":")
		if !ok || !profileSiteDigestValid(digest) || len(row.Resource) > 512 || !wirelessTagPrivateSelected(row.Operation.Resource) {
			return nil, errConfigurationOwned
		}
		row.Resource = original
		groups[digest] = append(groups[digest], *row.DeepCopy())
	}
	visited := map[string]bool{}
	for _, creator := range current {
		if creator.Operation.Action != "Create" {
			continue
		}
		child := creator
		for wirelessTagRecreateSelected(child.Operation.Resource) {
			digest, ok := wirelessTagRecreateParent(child.Operation.Resource)
			rows, exists := groups[digest]
			if !ok || !exists || visited[digest] {
				return nil, errConfigurationOwned
			}
			local := &cvk.NetworkControllerConfigurationStatus{Owners: state.Owners, ActionApplications: rows}
			previous, err := wirelessTagCurrentRecords(local)
			if err != nil {
				return nil, err
			}
			ns := strings.SplitN(child.Operation.Resource, "/", 2)[0] + "/"
			keys := []string{}
			for _, effect := range child.EffectReceipts {
				keys = append(keys, strings.Split(effect, ":")[0])
			}
			cohort, err := wirelessTagRecreationCandidateRecords(previous, ns, keys, &child.Operation)
			_, childKey, _, _ := wirelessTagPrivateParts(child.Operation.Resource)
			if err != nil || cohort.Digest != digest || cohort.ComparisonKeyID != childKey || len(cohort.Rows) != len(rows) {
				return nil, errConfigurationOwned
			}
			visited[digest], out[digest] = true, cohort
			for _, row := range rows {
				if row.Operation.Action == "Create" {
					child = row
					break
				}
			}
		}
	}
	if len(visited) != len(groups) {
		return nil, errConfigurationOwned
	}
	// The appliance may reuse a tag identifier after delete/recreate. Its
	// authenticated incarnation is the lineage, not a promise of fresh UUIDs.
	// One native key still cannot alias a different logical member.
	used := map[string]string{}
	claim := func(ns, parent, id, logical string) bool {
		key := ns + parent + "\x00" + id
		if old, exists := used[key]; exists && old != logical {
			return false
		}
		used[key] = logical
		return true
	}
	latest, err := wirelessTagLatest(current)
	if err != nil {
		return nil, err
	}
	for key, old := range latest {
		ns := strings.SplitN(key, "/", 2)[0] + "/"
		if !claim(ns, old.row.Operation.ParentID, old.id, key) {
			return nil, errConfigurationOwned
		}
	}
	for _, cohort := range out {
		for key, id := range cohort.NativeIDs {
			if !claim(cohort.Namespace, cohort.ParentID, id, cohort.Namespace+key) {
				return nil, errConfigurationOwned
			}
		}
	}
	return out, nil
}
