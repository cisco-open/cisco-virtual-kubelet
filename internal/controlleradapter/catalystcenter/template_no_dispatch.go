// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Only metadata projects/drafts/commits can retire a proven unsubmitted action.
// Deployment, membership and tag operations keep their distinct protocols. The
// caller must also possess the original in-memory guard capability; syntax and
// an empty task ID alone never prove absence of a native request.
func templateNoDispatchKindSupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Section != "templates" || len(op.Resource) > 512 {
		return false
	}
	if strings.HasPrefix(op.Resource, templateApplyPrefix) {
		_, _, _, ok := templateApplyIdentity(op)
		return ok
	}
	if strings.HasPrefix(op.Resource, templateCreatePrefix) {
		return validTemplateCreateOperation(op)
	}
	resource := strings.TrimPrefix(op.Resource, "objects/")
	validName := func(name string) bool {
		return name != "" && strings.TrimSpace(name) == name && len(name) <= 256 && !strings.ContainsAny(name, "#\x00\r\n")
	}
	validAction := op.Action == "Create" && op.TargetID == "" || op.Action == "Update" && validAPIID(op.TargetID)
	if op.ParentID == "" {
		return resource != "Onboarding Configuration" && validName(resource) && validAction
	}
	if !validAPIID(op.ParentID) {
		return false
	}
	stage, key, ok := strings.Cut(resource, ":")
	if !ok || (stage != "draft" && stage != "commit") {
		return false
	}
	project, name, ok := strings.Cut(key, "#")
	if !ok || !validName(project) || !validName(name) {
		return false
	}
	return validAction && (stage == "draft" || op.Action == "Update")
}
