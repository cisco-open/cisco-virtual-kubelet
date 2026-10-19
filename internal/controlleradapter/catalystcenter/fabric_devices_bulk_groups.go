// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

const fabricDevicesBulkAnnotation = "cisco.vk/catalyst-center-bulk-fabric-devices"

type fabricDevicesBulkBatch struct {
	site, zone string
	wants      []fabricDeviceIntent
}

func fabricDevicesBulkEnabled(config *configapi.NetworkControllerConfig) (bool, error) {
	if config == nil {
		return false, errConfigurationTransition
	}
	flag := config.Annotations[fabricDevicesBulkAnnotation]
	if flag != "" && flag != "true" && flag != "false" {
		return false, errConfigurationTransition
	}
	if flag == "true" && (len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete) {
		return false, errConfigurationTransition
	}
	return flag == "true", nil
}

// Match the pinned module's root-site and zone cohorts. A root cohort exists
// when that site has an unzoned declaration, and includes its zoned devices too.
// Sort and chunk the complete cohort, including already matching memberships;
// filtering only missing members would change batch identity after acceptance.
func groupFabricDevicesBulk(wants []fabricDeviceIntent) ([]fabricDevicesBulkBatch, error) {
	if len(wants) == 0 || len(wants) > 1000 {
		return nil, errConfigurationTransition
	}
	roots := map[string]bool{}
	for _, want := range wants {
		if want.fabricZone == "" {
			roots[want.fabricSite] = true
		}
	}
	groups := map[string]*fabricDevicesBulkBatch{}
	add := func(want fabricDeviceIntent, zone string) {
		key := want.fabricSite + "\x00" + zone
		if groups[key] == nil {
			groups[key] = &fabricDevicesBulkBatch{site: want.fabricSite, zone: zone}
		}
		want.fabricZone = zone
		groups[key].wants = append(groups[key].wants, want)
	}
	for _, want := range wants {
		if roots[want.fabricSite] {
			add(want, "")
		}
		if want.fabricZone != "" {
			add(want, want.fabricZone)
		}
	}
	ordered := []*fabricDevicesBulkBatch{}
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if (ordered[i].zone == "") != (ordered[j].zone == "") {
			return ordered[i].zone == ""
		}
		if ordered[i].site != ordered[j].site {
			return ordered[i].site < ordered[j].site
		}
		return ordered[i].zone < ordered[j].zone
	})
	out := []fabricDevicesBulkBatch{}
	for _, group := range ordered {
		sort.Slice(group.wants, func(i, j int) bool {
			return group.wants[i].inventory.reference.Name < group.wants[j].inventory.reference.Name
		})
		for start := 0; start < len(group.wants); start += fabricDevicesBulkLimit {
			end := min(start+fabricDevicesBulkLimit, len(group.wants))
			out = append(out, fabricDevicesBulkBatch{site: group.site, zone: group.zone, wants: append([]fabricDeviceIntent(nil), group.wants[start:end]...)})
		}
	}
	return out, nil
}

// Public preparation does not claim devices or dispatch. Observe every batch
// twice, then use the existing source-bound preparation and shared journal CAS.
// In particular, a late zone/peer failure cannot leave an earlier batch prepared.
func (r *configReconciler) prepareConfiguredFabricDevicesBulk(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, batches []fabricDevicesBulkBatch) (bool, error) {
	if !r.a.configurationGroupAdmissionReady() {
		return false, errConfigurationDeviceAdmission
	}
	nc, err := r.a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return false, errConfigurationBusy
	}
	preflightCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	type observation struct {
		native  *fabricDevicesBulkSnapshot
		targets []contract.GroupTargetIdentity
	}
	collect := func() ([]observation, error) {
		out := []observation{}
		ids, physical := map[string]string{}, map[string]string{}
		bindings := map[string]contract.GroupTargetIdentity{}
		footprints := map[string]contract.GroupTargetIdentity{}
		parents := map[string]*fabricDevicesParentProposal{}
		for _, batch := range batches {
			var proposal *fabricDevicesParentProposal
			if batch.zone != "" {
				proposal = parents[batch.site]
			}
			native, targets, err := r.a.client.fabricDevicesBulkPreflightTargets(preflightCtx, r.a.statusReader, config.Namespace, batch.wants, proposal)
			if err != nil {
				return nil, err
			}
			if native.mask != 0 && len(native.resource(native.mask)) > 512 {
				return nil, errConfigurationTransition
			}
			for _, target := range targets {
				if previous, exists := footprints[target.PhysicalIdentity]; exists && !reflect.DeepEqual(previous, target) {
					return nil, errConfigurationStale
				}
				footprints[target.PhysicalIdentity] = target
			}
			if batch.zone == "" {
				if parents[batch.site] == nil {
					parents[batch.site] = &fabricDevicesParentProposal{fabricID: native.fabric, members: map[string]fabricDeviceConfiguration{}}
				}
				if parents[batch.site].fabricID != native.fabric {
					return nil, errConfigurationStale
				}
				for _, entry := range native.entries {
					parents[batch.site].members[entry.target.ID] = entry.native
				}
			}
			names := map[string]string{}
			for _, want := range batch.wants {
				target, err := resolveConfigurationDevice(want.inventory.reference, native.physical)
				if err != nil {
					return nil, err
				}
				names[target.ID] = want.inventory.reference.Name
			}
			for _, entry := range native.entries {
				name := names[entry.target.ID]
				if name == "" {
					return nil, errConfigurationStale
				}
				for _, target := range targets {
					if target.InventoryID != entry.target.ID {
						continue
					}
					if ids[target.InventoryID] != "" && ids[target.InventoryID] != name || physical[target.PhysicalIdentity] != "" && physical[target.PhysicalIdentity] != name {
						return nil, errConfigurationOwned
					}
					if previous, exists := bindings[name]; exists && !reflect.DeepEqual(previous, target) {
						return nil, errConfigurationStale
					}
					bindings[name] = target
					ids[target.InventoryID], physical[target.PhysicalIdentity] = name, name
				}
			}
			out = append(out, observation{native, targets})
		}
		return out, nil
	}
	before, err := collect()
	if err != nil {
		return false, err
	}
	fresh, err := collect()
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(before, fresh) {
		return false, errConfigurationStale
	}
	if err := r.a.checkConfigurationSnapshot(preflightCtx, config, source); err != nil {
		return false, err
	}
	for i, batch := range batches {
		if before[i].native.deferredParent {
			return false, errConfigurationBusy
		}
		if before[i].native.mask == 0 {
			continue
		}
		op, err := r.a.prepareFabricDevicesBulkObserved(preflightCtx, config, source, batch.wants, func(context.Context) error {
			current, err := collect()
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before, current) {
				return errConfigurationStale
			}
			return nil
		})
		if err == nil && op == nil {
			return false, errConfigurationStale
		}
		return true, err
	}
	return false, nil
}
