// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type compositeTemplateTarget struct {
	template                 nacmodel.TemplateIdentity
	redeploy                 string
	copyingConfig, forcePush bool
	parameters               map[string]map[string][]string
	comparison               *templateInputComparison
}

// The key hashes only physical/native identities and nonsecret options. The
// ordered version graph itself remains explicitly recoverable in the journal.
func compositeTemplateDeploymentResource(d *compositeTemplateDependencies, target compositeTemplateTarget) (string, error) {
	if d == nil || d.device == nil || d.content == nil {
		return "", errConfigurationStale
	}
	bindings, err := d.content.versionBindings()
	if err != nil {
		return "", err
	}
	projects := []string{d.content.parent.base.ProjectID}
	for _, member := range d.content.members {
		projects = append(projects, member.base.ProjectID)
	}
	raw, err := json.Marshal(struct {
		Projects       []string
		Physical       string
		Provision      wiredProvisionedDevice
		Platform       templateDevicePlatform
		Versions       *cvk.NetworkControllerConfigurationDependencyVersions
		Copying, Force bool
	}{projects, d.device.digest, d.device.before, d.platform, bindings, target.copyingConfig, target.forcePush})
	if err != nil {
		return "", err
	}
	resource := fmt.Sprintf("template-composite-deploy:%x", sha256.Sum256(raw))
	if target.comparison == nil {
		return resource, nil
	}
	payload, err := d.payload(target.copyingConfig, target.forcePush, target.parameters)
	if err != nil {
		return "", err
	}
	return target.comparison.bind(resource, payload)
}

// Private native primitive. Callers must supply owned journal state and a
// physical dispatch permit before invoking submit. Public routing is gated.
func (c *client) planCompositeTemplateDeployment(ctx context.Context, device inventoryDeviceIntent, target compositeTemplateTarget) (*configurationWrite, error) {
	if !validTemplateRedeploy(target.redeploy) {
		return nil, errConfigurationTransition
	}
	snapshot, err := c.compositeTemplateDependenciesAtVersion(ctx, device, target.template, nil)
	if err != nil {
		return nil, err
	}
	payload, err := snapshot.payload(target.copyingConfig, target.forcePush, target.parameters)
	if err != nil {
		return nil, err
	}
	resource, err := compositeTemplateDeploymentResource(snapshot, target)
	if err != nil {
		return nil, err
	}
	bindings, err := snapshot.content.versionBindings()
	if err != nil {
		return nil, err
	}
	return &configurationWrite{action: "Update", resource: resource, identity: configurationResourceIdentity{target: snapshot.device.target.ID, parent: snapshot.device.site.ID, serial: snapshot.device.target.Serial, dependencies: bindings}, submit: func(ctx context.Context) (Task, error) {
		if err := c.revalidateCompositeTemplateDependencies(ctx, device, target.template, snapshot); err != nil {
			return Task{}, err
		}
		if target.comparison != nil {
			if target.comparison.check == nil {
				return Task{}, errConfigurationSecret
			}
			if err := target.comparison.check(ctx); err != nil {
				return Task{}, err
			}
		}
		raw, err := c.mutate(ctx, http.MethodPost, templateDeployPath, payload)
		if err != nil {
			return Task{}, err
		}
		id, err := templateDeploymentReceipt(raw)
		if err != nil {
			return Task{}, err
		}
		return Task{ID: id}, nil
	}}, nil
}

// Observation only: bind the retained parent/member versions and the physical
// incarnation before reading deployment status. Never POST or select latest.
func (c *client) observeCompositeTemplateDeployment(ctx context.Context, device inventoryDeviceIntent, target compositeTemplateTarget, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if op == nil || (op.Phase != "Accepted" && op.Phase != "Verifying") || op.Section != "inventory" || op.Action != "Update" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.Before(op.SubmittedAt.Time) || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || !validTemplateRedeploy(target.redeploy) {
		return false, errConfigurationTransition
	}
	if _, err := canonicalDeploymentID(op.TaskID); err != nil {
		return false, err
	}
	pinned, err := parseCompositeDeploymentBindings(op.DependencyVersions)
	if err != nil {
		return false, err
	}
	snapshot, err := c.compositeTemplateDependenciesAtVersion(ctx, device, target.template, pinned)
	if err != nil {
		return false, err
	}
	resource, err := compositeTemplateDeploymentResource(snapshot, target)
	if err != nil {
		return false, err
	}
	bindings, err := snapshot.content.versionBindings()
	if err != nil {
		return false, err
	}
	if resource != op.Resource || !reflect.DeepEqual(bindings, op.DependencyVersions) || op.TargetID != snapshot.device.target.ID || op.ParentID != snapshot.device.site.ID || op.TargetSerial != snapshot.device.target.Serial {
		return false, errConfigurationStale
	}
	if _, err := snapshot.payload(target.copyingConfig, target.forcePush, target.parameters); err != nil {
		return false, err
	}
	return c.templateDeploymentComplete(ctx, templateDeploymentIdentity{deploymentID: op.TaskID, deviceID: op.TargetID, project: target.template.Project, name: target.template.Name, version: pinned.parent.version.Number}, op.SubmittedAt.Time, now)
}
