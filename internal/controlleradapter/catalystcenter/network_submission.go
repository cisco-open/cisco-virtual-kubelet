// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"slices"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type networkSubmissionSite struct{ id, parent, kind, path string }
type networkSubmissionObservation struct {
	sites               map[string]networkSubmissionSite
	explicit, effective map[string]networkObservation
}

func (networkSubmissionObservation) String() string { return "<private network settings observation>" }
func (networkSubmissionObservation) GoString() string {
	return "<private network settings observation>"
}

func freezeNetworkExecutor(e *networkConfigurationExecutor) *networkConfigurationExecutor {
	frozen := &networkConfigurationExecutor{client: e.client, bindings: make([]networkBinding, len(e.bindings)), applicationPlan: e.applicationPlan, applicationProof: e.applicationProof}
	if e.comparison != nil {
		copy := *e.comparison
		copy.key = slices.Clone(e.comparison.key)
		frozen.comparison = &copy
	}
	for i, binding := range e.bindings {
		frozen.bindings[i] = networkBinding{path: binding.path, kind: binding.kind, settings: make([]networkSetting, len(binding.settings))}
		for j, setting := range binding.settings {
			copy := setting
			copy.servers = slices.Clone(setting.servers)
			copy.telemetry = cloneNetworkTelemetry(setting.telemetry)
			frozen.bindings[i].settings[j] = copy
		}
	}
	return frozen
}

// Capture only selected settings and their actual inheritance dependencies.
// Each native site/kind is read once within one observation. Reuse the common
// inheritance resolver; unrelated settings/branches do not widen ownership.
func (e *networkConfigurationExecutor) observeSubmission(ctx context.Context) (networkSubmissionObservation, error) {
	zero := networkSubmissionObservation{}
	sites, err := e.sites(ctx)
	if err != nil {
		return zero, err
	}
	result := networkSubmissionObservation{sites: map[string]networkSubmissionSite{}, explicit: map[string]networkObservation{}, effective: map[string]networkObservation{}}
	byID := map[string]Site{}
	for _, site := range sites {
		byID[site.ID] = site
	}
	bindSite := func(site Site) {
		result.sites[site.NameHierarchy] = networkSubmissionSite{site.ID, site.ParentID, site.Type, site.NameHierarchy}
	}
	read := func(ctx context.Context, id, kind string) (networkObservation, error) {
		site, ok := byID[id]
		if !ok {
			return networkObservation{}, errConfigurationStale
		}
		key := networkSettingIdentity(site.NameHierarchy, kind)
		if got, ok := result.explicit[key]; ok {
			return cloneNetworkObservation(got), nil
		}
		got, err := e.client.readNetworkSetting(ctx, id, kind)
		if err != nil {
			return networkObservation{}, err
		}
		bindSite(site)
		result.explicit[key] = got
		return cloneNetworkObservation(got), nil
	}
	for _, binding := range e.bindings {
		if len(binding.settings) == 0 {
			continue
		}
		site := sites[binding.path]
		// Preserve the exact parent incarnation even for explicit child overrides.
		for depth := 0; ; depth++ {
			if depth >= len(sites) {
				return zero, errInvalidResponse
			}
			bindSite(site)
			if site.NameHierarchy == "Global" {
				break
			}
			parent, ok := sites[parentSitePath(site.NameHierarchy)]
			if !ok || parent.ID != site.ParentID {
				return zero, errConfigurationStale
			}
			site = parent
		}
		for _, setting := range binding.settings {
			key := networkSettingIdentity(binding.path, setting.kind)
			if _, exists := result.effective[key]; exists {
				return zero, errConfigurationTransition
			}
			got, err := effectiveNetworkSettingWithReader(ctx, sites, binding.path, setting.kind, read)
			if err != nil {
				return zero, err
			}
			result.effective[key] = got
		}
	}
	return result, nil
}

func networkSettingNoDispatchOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Action != "Update" || !validAPIID(op.TargetID) || op.ParentID != "" {
		return false
	}
	_, ok := networkSettingResourceKey(op.Resource)
	return ok
}

func cloneNetworkTelemetry(in map[string]map[string]any) map[string]map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]any, len(in))
	for group, fields := range in {
		if fields == nil {
			out[group] = nil
			continue
		}
		out[group] = make(map[string]any, len(fields))
		for key, value := range fields {
			// Parsed telemetry values are scalars or canonical string lists.
			if list, ok := value.([]string); ok {
				value = slices.Clone(list)
			}
			out[group][key] = value
		}
	}
	return out
}
func cloneNetworkObservation(in networkObservation) networkObservation {
	out := in
	out.servers = slices.Clone(in.servers)
	out.telemetry = cloneNetworkTelemetry(in.telemetry)
	return out
}
