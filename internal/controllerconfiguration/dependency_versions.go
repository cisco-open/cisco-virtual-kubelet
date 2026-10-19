// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"regexp"
	"strings"
)

var dependencyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var compositeResourcePattern = regexp.MustCompile(`^template-composite-deploy:[a-f0-9]{64}(:private-v1:[a-f0-9]{64}:[a-f0-9]{64})?$`)

// ValidateDependencyVersions checks ordered nonsecret version identities. This
// is shared by adapter decoding and reservation binding; it grants no authority.
func ValidateDependencyVersions(value *cvk.NetworkControllerConfigurationDependencyVersions) error {
	reject := errors.New("invalid configuration dependency version graph")
	if value == nil || value.Kind != "template-graph-v1" || len(value.Entries) < 2 || len(value.Entries) > 1001 {
		return reject
	}
	seen := map[string]bool{}
	for _, entry := range value.Entries {
		if !dependencyIDPattern.MatchString(entry.ID) || !dependencyIDPattern.MatchString(entry.VersionID) || entry.ID == entry.VersionID || entry.Version < 1 || seen[entry.ID] || seen[entry.VersionID] {
			return reject
		}
		seen[entry.ID], seen[entry.VersionID] = true, true
	}
	return nil
}
func validateOperationDependencies(op *cvk.NetworkControllerConfigurationOperation) error {
	reject := errors.New("dependency graph is outside supported physical operation scope")
	composite := strings.HasPrefix(op.Resource, "template-composite-deploy:")
	if op.DependencyVersions == nil {
		if composite {
			return reject
		}
		return nil
	}
	if !compositeResourcePattern.MatchString(op.Resource) || op.Section != "inventory" || op.Action != "Update" || !dependencyIDPattern.MatchString(op.TargetID) || !dependencyIDPattern.MatchString(op.ParentID) || op.TargetSerial == "" || op.PhysicalGroup != nil || len(op.CredentialVersions) != 0 {
		return reject
	}
	return ValidateDependencyVersions(op.DependencyVersions)
}
