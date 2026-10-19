// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "strings"

// Scope version graphs to composite physical deployments. No global resource,
// credential operation or existing regular deployment may silently adopt one.
// This validates journal data only; physical admission remains separate.
func validateConfigurationDependencyVersions(section, resource, action string, identity configurationResourceIdentity) error {
	if err := validateSSIDCredentialHistory(section, resource, identity.credentials, identity.ssidCredentialHistory); err != nil {
		return err
	}
	composite := strings.HasPrefix(resource, "template-composite-deploy:")
	if identity.dependencies == nil {
		if composite {
			return errConfigurationTransition
		}
		return nil
	}
	if !composite || section != "inventory" || action != "Update" || !validAPIID(identity.target) || !validAPIID(identity.parent) || identity.serial == "" || len(identity.credentials) != 0 {
		return errConfigurationTransition
	}
	_, err := parseCompositeDeploymentBindings(identity.dependencies)
	return err
}
