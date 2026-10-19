// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"errors"
	"slices"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func combinedFabricIPAMSections(sections []string) bool {
	return len(sections) == 2 && slices.Contains(sections, "network_settings") && slices.Contains(sections, "fabric")
}

func networkIntentProjection(root map[string]any) ([]byte, error) {
	projection := map[string]any{"network_settings": root["network_settings"]}
	if sites, present := root["sites"]; present {
		projection["sites"] = sites
	}
	return json.Marshal(map[string]any{"catalyst_center": projection})
}

func bindFabricIPAMPlanning(fabric *fabricConfiguration, ipam *ipamConfigurationExecutor) {
	if ipam.count() == 0 {
		return
	}
	for _, deployment := range fabric.gateways {
		deployment.ipam = ipam
	}
}

func (c *client) compileFabricIPAMApply(raw []byte, sections []string) (configurationApplyPlan, error) {
	zero := configurationApplyPlan{}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return zero, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return zero, errInvalidResponse
	}
	for key := range root {
		if key != "fabric" && key != "network_settings" && key != "sites" {
			return zero, errors.New("combined fabric/IPAM source contains unselected sections")
		}
	}
	networkRaw, err := networkIntentProjection(root)
	if err != nil {
		return zero, err
	}
	intent, err := parseNetworkReportIntent(networkRaw)
	if err != nil {
		return zero, err
	}
	network, err := c.compileNetworkSettingsIntent(intent)
	if err != nil {
		return zero, err
	}
	fabricRaw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": root["fabric"]}})
	if err != nil {
		return zero, err
	}
	fabric, err := c.compileFabricConfiguration(fabricRaw)
	if err != nil {
		return zero, err
	}
	bindFabricIPAMPlanning(fabric, network.ipam)
	// Both sections retain the existing atomic section ownership. The canonical
	// fabric journal and explicit part prefixes are independent of input order.
	plan := configurationApplyPlan{section: "fabric", executor: &configurationGroup{parts: []configurationPart{{name: "network_settings", executor: network}, {name: "fabric", executor: fabric}}}}
	if _, anchored := fabric.configurationExecutor.(*fabricAnchorAdmissionGate); anchored {
		plan.executor = &fabricAnchorAdmissionGate{configurationExecutor: plan.executor}
	}
	if _, extranet := fabric.configurationExecutor.(*fabricExtranetAdmissionGate); extranet {
		plan.executor = &fabricExtranetAdmissionGate{configurationExecutor: plan.executor}
	}
	for _, section := range sections {
		count := network.count()
		if section == "fabric" {
			count = fabric.count()
		}
		plan.sections = append(plan.sections, configapi.NetworkControllerSectionStatus{Name: section, ResourceCount: int64(count)})
	}
	return plan, nil
}
