// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (e *networkProfileConfiguration) bindWirelessTagRecreation(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) error {
	p := e.tagPrune
	p.recreate = map[string]wirelessTagRecreationCohort{}
	for _, tags := range e.tagExecutors {
		ns := "wireless-site-tags/"
		if tags.policy {
			ns = "wireless-policy-tags/"
		}
		for _, profile := range tags.desired {
			groups := [][]string{}
			policyKeys := []string{}
			for _, tag := range tags.tags(profile) {
				if len(tag.sites) == 0 {
					continue
				}
				key, _ := wirelessTagDigest([]string{profile.name, tag.name})
				if tags.policy {
					policyKeys = append(policyKeys, key)
				} else {
					groups = append(groups, []string{key})
				}
			}
			if len(policyKeys) > 0 {
				groups = append(groups, policyKeys)
			}
			for _, keys := range groups {
				parent := ""
				for _, key := range keys {
					if old, exists := p.latest[ns+key]; exists && old.row.Operation.Action == "Delete" {
						parent = old.row.Operation.ParentID
					}
				}
				if parent == "" {
					continue
				}
				if !p.enabled {
					return errConfigurationOwned
				}
				next := &cvk.NetworkControllerConfigurationOperation{Section: "network_profiles", Action: "Create", ParentID: parent, ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version}
				cohort, err := wirelessTagRecreationCandidate(nc.Status.Configuration, ns, keys, next)
				if err != nil {
					return err
				}
				for _, key := range keys {
					p.recreate[ns+key] = cohort
				}
			}
		}
	}
	return nil
}

func (e *wirelessTagPruneExecutor) recreationGroup(ns string, members []string) (wirelessTagRecreationCohort, bool, error) {
	var out wirelessTagRecreationCohort
	keys := []string{}
	selected := 0
	for _, member := range members {
		key := strings.Split(member, ":")[0]
		keys = append(keys, key+":")
		if cohort, exists := e.recreate[ns+key]; exists {
			if selected > 0 && cohort.Digest != out.Digest {
				return out, false, errConfigurationOwned
			}
			out = cohort
			selected++
		}
	}
	if selected == 0 {
		return out, false, nil
	}
	expected := []string{}
	for _, key := range out.Keys {
		expected = append(expected, key+":")
	}
	if selected != len(members) || wirelessTagGroupKey(keys) != wirelessTagGroupKey(expected) {
		return out, false, errConfigurationOwned
	}
	return out, true, nil
}

// Stage only a copy. The existing receipt writer appends the verified successor
// and persists both changes in one resource-version-guarded status update.
func wirelessTagArchivePredecessor(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, effects []string) (*cvk.NetworkControllerConfigurationStatus, error) {
	if state == nil || op == nil || !wirelessTagRecreateSelected(op.Resource) || !wirelessTagPrivateEnvelope(op) || op.Phase != "Verifying" || !reflect.DeepEqual(state.Operation, op) {
		return nil, errConfigurationOwned
	}
	if len(state.ActionApplications) >= 256 {
		return nil, errConfigurationBusy
	}
	keys := []string{}
	ids := map[string]string{}
	for _, effect := range effects {
		p := strings.Split(effect, ":")
		if len(p) != 3 || !profileSiteDigestValid(p[0]) || !validAPIID(p[1]) || !profileSiteDigestValid(p[2]) || ids[p[1]] != "" {
			return nil, errConfigurationOwned
		}
		keys = append(keys, p[0])
		ids[p[1]] = p[0]
	}
	if wirelessTagGroupKey(effects) != wirelessTagProducedGroup(op.Resource) {
		return nil, errConfigurationOwned
	}
	ns := strings.SplitN(op.Resource, "/", 2)[0] + "/"
	cohort, err := wirelessTagRecreationCandidate(state, ns, keys, op)
	digest, ok := wirelessTagRecreateParent(op.Resource)
	_, keyID, _, _ := wirelessTagPrivateParts(op.Resource)
	if err != nil || !ok || cohort.Digest != digest || cohort.ComparisonKeyID != keyID {
		return nil, errConfigurationOwned
	}
	for key, id := range cohort.NativeIDs {
		if successor, reused := ids[id]; reused && successor != key {
			return nil, errConfigurationOwned
		}
	}
	for _, row := range state.ActionApplications {
		if row.Operation.TaskID == op.TaskID {
			return nil, errConfigurationOwned
		}
	}
	current, err := wirelessTagCurrentRecords(state)
	if err != nil {
		return nil, err
	}
	retired, err := wirelessTagRetiredCohorts(state, current)
	if err != nil {
		return nil, err
	}
	for _, old := range retired {
		if old.Namespace == ns && old.ParentID == op.ParentID {
			for key, id := range old.NativeIDs {
				if successor, reused := ids[id]; reused && successor != key {
					return nil, errConfigurationOwned
				}
			}
		}
	}
	out := state.DeepCopy()
	archived := 0
	for i := range out.ActionApplications {
		row := &out.ActionApplications[i]
		for _, original := range cohort.Rows {
			if reflect.DeepEqual(*row, original) {
				row.Resource = wirelessTagRetiredPrefix + cohort.Digest + ":" + row.Resource
				if len(row.Resource) > 512 {
					return nil, errConfigurationOwned
				}
				archived++
				break
			}
		}
	}
	if archived != len(cohort.Rows) {
		return nil, errConfigurationOwned
	}
	return out, nil
}
