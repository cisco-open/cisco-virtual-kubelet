// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Encode only identities, never content or parameters. Admission of this
// envelope grants no authority; shared physical admission is required.
func (d *compositeDeploymentContent) versionBindings() (*cvk.NetworkControllerConfigurationDependencyVersions, error) {
	if d == nil || d.parent == nil {
		return nil, errConfigurationStale
	}
	out := &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1"}
	all := append([]*regularTemplateContent{d.parent}, d.members...)
	for _, member := range all {
		if member == nil {
			return nil, errConfigurationStale
		}
		out.Entries = append(out.Entries, cvk.NetworkControllerConfigurationDependencyVersion{ID: member.base.ID, VersionID: member.version.ID, Version: member.version.Number})
	}
	if _, err := parseCompositeDeploymentBindings(out); err != nil {
		return nil, err
	}
	return out, nil
}
func parseCompositeDeploymentBindings(value *cvk.NetworkControllerConfigurationDependencyVersions) (*compositeDeploymentVersionBinding, error) {
	if err := contract.ValidateDependencyVersions(value); err != nil {
		return nil, errConfigurationTransition
	}
	out := &compositeDeploymentVersionBinding{}
	for i, entry := range value.Entries {
		binding := templateDeploymentVersionBinding{baseID: entry.ID, version: templateVersion{ID: entry.VersionID, Number: entry.Version}}
		if i == 0 {
			out.parent = binding
		} else {
			out.members = append(out.members, binding)
		}
	}
	return out, nil
}
