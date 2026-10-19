// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

const lanLinkDeleteFamily = "lan-link-delete:"
const lanLinkDeletePrefix = lanLinkDeleteFamily + "v1:"
const lanLinkDeleteScopedPrefix = lanLinkDeleteFamily + "v2:"

type lanLinkDeleteBinding struct{ logical, owner, physical, declaration, scope, prepared string }

func lanLinkHexParts(value, prefix string, count int) ([]string, error) {
	if !strings.HasPrefix(value, prefix) || len(value) > 512 {
		return nil, errConfigurationTransition
	}
	parts := strings.Split(strings.TrimPrefix(value, prefix), ":")
	if len(parts) != count {
		return nil, errConfigurationTransition
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return nil, errConfigurationTransition
		}
		if _, err := hex.DecodeString(part); err != nil {
			return nil, errConfigurationTransition
		}
	}
	return parts, nil
}

func parseLANLinkDeleteResource(resource, action string) (lanLinkDeleteBinding, error) {
	var zero lanLinkDeleteBinding
	if action != "Update" {
		return zero, errConfigurationTransition
	}
	prefix, count := lanLinkDeletePrefix, 5
	if strings.HasPrefix(resource, lanLinkDeleteScopedPrefix) {
		prefix, count = lanLinkDeleteScopedPrefix, 6
	}
	p, err := lanLinkHexParts(resource, prefix, count)
	if err != nil {
		return zero, err
	}
	result := lanLinkDeleteBinding{logical: p[0], owner: p[1], physical: p[2], declaration: p[3], prepared: p[len(p)-1]}
	if count == 6 {
		result.scope = p[4]
	}
	return result, nil
}

func lanLinkDigest(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func lanLinkDeclarationDigest(want lanLinkIntent) string { return lanLinkDigest(wantJSON(want)) }

// v2 retains the exact raw declaration AND its pool-independent identity.
// The latter is read-only post-completion semantics, never submission authority.
func lanLinkDeclarationScope(want lanLinkIntent) string {
	want.pool = ""
	return lanLinkDeclarationDigest(want)
}
func exactLANLinkDeleteDeclaration(want lanLinkIntent, binding lanLinkDeleteBinding) bool {
	return lanLinkDeclarationDigest(want) == binding.declaration && (binding.scope == "" || lanLinkDeclarationScope(want) == binding.scope)
}
func observedLANLinkDeleteDeclaration(want lanLinkIntent, binding lanLinkDeleteBinding) bool {
	if binding.scope != "" {
		return lanLinkDeclarationScope(want) == binding.scope
	}
	return lanLinkDeclarationDigest(want) == binding.declaration
}

func wantJSON(w lanLinkIntent) []string {
	return []string{w.name, w.action, w.primaryIP, w.primaryInterface, w.secondaryIP, w.secondaryInterface, w.pool}
}

// Delete recovery binds the admitted physical endpoints independently of a
// pool which may legitimately be retired after deconfiguration. It is not
// proof of absence or permission to delete an unowned link.
func lanLinkDeletePhysicalDigest(endpoints *lanLinkEndpointSnapshot) (string, error) {
	if endpoints == nil {
		return "", errConfigurationTransition
	}
	type identity struct {
		Device Device
		Port   lanLinkInterface
	}
	project := func(e lanLinkEndpoint) identity {
		p := e.port
		p.AdminStatus, p.Status, p.Mode, p.IPv4, p.Mask, p.ISIS, p.VLAN, p.NativeVLAN = "", "", "", "", "", "", "", ""
		d := e.device
		d.APEthernetMAC, d.Role, d.RoleSource, d.Reachability = "", "", "", ""
		return identity{d, p}
	}
	return lanLinkDigest([]identity{project(endpoints.primary), project(endpoints.secondary)}), nil
}

func validateLANLinkDeleteCompletionOperation(op *cvk.NetworkControllerConfigurationOperation, now time.Time) (lanLinkDeleteBinding, error) {
	var zero lanLinkDeleteBinding
	if op == nil || op.Section != "lan_automation" || op.TargetID == op.ParentID || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.CreatedResourceID != "" || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CancellationRequestedAt != nil || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != 2 {
		return zero, errConfigurationTransition
	}
	return parseLANLinkDeleteResource(op.Resource, op.Action)
}

func validLANLinkMutationResource(resource, action string) bool {
	if validLANLinkAddResource(resource, action) {
		return true
	}
	_, err := parseLANLinkDeleteResource(resource, action)
	return err == nil
}
