// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const globalAuthResourcePrefix = "auth-profile:v2:"

var errGlobalAuthBinding = errors.New("global authentication result binding is missing or changed")

func authConfigurationValueDigest(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Only nonsensitive modeled fields and preserved native booleans enter identity.
// Description is upstream-unmanaged and never hashed, logged or journaled.
func authProfileConfigurationDigest(p authProfile) string {
	p.ID, p.FabricID = "", ""
	if p.ACL != nil {
		acl := *p.ACL
		acl.Description = nil
		if acl.Contracts != nil {
			contracts := slices.Clone(*acl.Contracts)
			slices.SortFunc(contracts, func(a, b authContract) int { return strings.Compare(a.Port, b.Port) })
			acl.Contracts = &contracts
		}
		p.ACL = &acl
	}
	return authConfigurationValueDigest(p)
}

func authExpectedResultProfile(want, actual authProfile) authProfile {
	result := want
	result.Voice = actual.Voice
	if result.BPDU == nil {
		result.BPDU = actual.BPDU
	}
	return result
}

func globalAuthOperationResource(want, actual authProfile) string {
	return globalAuthResourcePrefix + strings.Join([]string{
		authConfigurationValueDigest(want.Name),
		authProfileConfigurationDigest(actual),
		authProfileConfigurationDigest(authExpectedResultProfile(want, actual)),
	}, ":")
}

func parseGlobalAuthOperationResource(resource string) ([]string, error) {
	suffix, ok := strings.CutPrefix(resource, globalAuthResourcePrefix)
	parts := strings.Split(suffix, ":")
	if !ok || len(parts) != 3 {
		return nil, errGlobalAuthBinding
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return nil, errGlobalAuthBinding
		}
		if _, err := hex.DecodeString(part); err != nil {
			return nil, errGlobalAuthBinding
		}
	}
	return parts, nil
}

func legacyGlobalAuthResource(resource string) bool {
	return resource == "Closed Authentication" || resource == "Open Authentication" || resource == "Low Impact"
}

// The shared journal may retire Prepared without dispatch. A Verified operation
// must retain a canonical binding or the exact legacy literal and task/UUID
// evidence. Unknown versions cannot escape through generic metadata completion.
func validateGlobalAuthCompletionBinding(op *cvk.NetworkControllerConfigurationOperation) error {
	if op == nil || op.Section != "authentication_templates" || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 {
		return errGlobalAuthBinding
	}
	if legacyGlobalAuthResource(op.Resource) {
		return nil
	}
	_, err := parseGlobalAuthOperationResource(op.Resource)
	return err
}
