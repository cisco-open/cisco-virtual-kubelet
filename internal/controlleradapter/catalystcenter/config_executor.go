// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// A status-only transition completed. Re-read the journal/source before
// considering any native request; no write is returned in this reconciliation.
var errConfigurationReplan = errors.New("configuration bookkeeping advanced; replan required")

// configurationExecutor supplies resource semantics to the one shared journal
// state machine. It never owns retry, task lifecycle or Kubernetes permissions.
type configurationExecutor interface {
	count() int
	next(context.Context) (*configurationWrite, error)
	verify(context.Context, *ciskov1.NetworkControllerConfigurationOperation) (bool, error)
}

type configurationWrite struct {
	resource, action string
	identity         configurationResourceIdentity
	submit           func(context.Context) (Task, error)
	// Optional guard-specific, one-use proof that the native submit was not called.
	noDispatch func(error) *configurationPreSubmitRefusal
	// submitCompleted is reserved for APIs with a validated synchronous response.
	// Exactly one submission function must be set.
	submitCompleted func(context.Context) error
	// submitCreated returns a native UUID from a validated synchronous create.
	// The journal retains it atomically with Verifying before any readback.
	submitCreated func(context.Context) (string, error)
}

func (c *client) compileConfigurationExecutor(raw []byte, section string, templateFiles ...map[string]string) (configurationExecutor, error) {
	switch section {
	case "feature_templates":
		return c.compileWirelessFeatureConfiguration(raw)
	case "wireless":
		return c.compileWirelessConfiguration(raw)
	case "fabric":
		return c.compileFabricConfiguration(raw)
	case "system_settings":
		desired, err := parseExternalAuth(raw)
		if err != nil {
			return nil, err
		}
		return &externalAuthExecutor{client: c, desired: desired}, nil
	case "network_profiles":
		return c.compileNetworkProfileConfiguration(raw)
	case "inventory":
		intent, err := parseInventoryIntent(raw)
		if err != nil {
			return nil, err
		}
		if len(intent.devices) > 0 {
			return nil, errors.New("inventory device execution is not yet supported")
		}
		for _, tag := range intent.tags {
			if tag.payload["systemTag"] == true {
				return nil, errors.New("system tag creation is not qualified")
			}
		}
		if len(intent.discoveries) == 0 {
			return &tagExecutor{client: c, desired: intent.tags}, nil
		}
		discovery := &discoveryExecutor{client: c, desired: intent.discoveries}
		if len(intent.tags) == 0 {
			return discovery, nil
		}
		return &configurationGroup{parts: []configurationPart{{name: "tags", executor: &tagExecutor{client: c, desired: intent.tags}}, {name: "discoveries", executor: discovery}}}, nil
	case "templates":
		var files map[string]string
		if len(templateFiles) > 0 {
			files = templateFiles[0]
		}
		desired, err := parseTemplateIntent(raw, files)
		if err != nil {
			return nil, err
		}
		memberships := &templateMembershipExecutor{client: c, intent: desired}
		for _, definition := range desired.definitions {
			extra := 0
			if len(desired.tags) > 0 || memberships.count() > 0 {
				extra = len("objects/")
			}
			if len("commit:"+definition.identity.Key())+extra > 512 {
				return nil, errors.New("template operation identity exceeds journal bound")
			}
		}
		var objects configurationExecutor
		if len(desired.definitions) > 0 {
			objects = &templateExecutor{client: c, intent: desired}
		} else if len(desired.projects) > 0 {
			objects = &templateProjectExecutor{client: c, desired: desired.projects}
		}
		if len(desired.tags) == 0 && memberships.count() == 0 {
			if objects == nil {
				// Empty retained intent is a real no-op, not a nil executor. Prune
				// remains separately gated by retained ownership and deletion policy.
				objects = &templateProjectExecutor{client: c}
			}
			return objects, nil
		}
		for _, tag := range desired.tags {
			if tag.payload["systemTag"] == true {
				return nil, errors.New("system tag creation is not qualified")
			}
		}
		parts := []configurationPart{}
		if len(desired.tags) > 0 {
			parts = append(parts, configurationPart{name: "tags", executor: &tagExecutor{client: c, desired: desired.tags}})
		}
		if objects != nil {
			parts = append(parts, configurationPart{name: "objects", executor: objects})
		}
		if memberships.count() > 0 {
			parts = append(parts, configurationPart{name: "members", executor: memberships})
		}
		return &configurationGroup{parts: parts}, nil
	case "authentication_templates":
		desired, err := parseAuthTemplates(raw)
		if err != nil {
			return nil, err
		}
		return &authConfigurationExecutor{client: c, desired: desired}, nil
	case "sites":
		desired, placements, err := parseSitesWithPlannedAPs(raw)
		if err != nil {
			return nil, err
		}
		if err = validateSiteExecution(desired); err != nil {
			return nil, err
		}
		sites := &siteConfigurationExecutor{client: c, desired: desired}
		if len(placements) > 0 {
			return &sitePlannedAPExecutor{sites: sites, placements: &plannedAPExecutor{client: c, desired: placements}}, nil
		}
		return sites, nil
	case "network_settings":
		intent, err := parseNetworkReportIntent(raw)
		if err != nil {
			return nil, err
		}
		return c.compileNetworkSettingsIntent(intent)
	}
	return nil, errors.New("configuration executor is not implemented")
}

func (c *client) compileNetworkSettingsIntent(intent networkReportIntent) (*networkSectionExecutor, error) {
	if len(intent.credentials) > 0 {
		return nil, errors.New("global credential execution requires version-bound secret references")
	}
	for _, assignment := range intent.assignments {
		if assignment.name == "" {
			return nil, errors.New("credential inheritance removal is not qualified for execution")
		}
	}
	for _, b := range intent.bindings {
		for _, s := range b.settings {
			if len(networkSettingIdentity(b.path, s.kind)) > 512 {
				return nil, errors.New("network setting identity exceeds journal bound")
			}
			if _, err := networkSettingPayload(s); err != nil {
				return nil, err
			}
		}
	}
	if err := validateIPAMAllocation(intent, nil, nil); err != nil {
		return nil, err
	}
	for _, reservation := range intent.reservations {
		if len(ipamReservationIdentity(reservation.site, reservation.Name)) > 512 {
			return nil, errors.New("IPAM reservation identity exceeds journal bound")
		}
	}
	return &networkSectionExecutor{assignments: &siteCredentialExecutor{client: c, desired: intent.assignments}, settings: &networkConfigurationExecutor{client: c, bindings: intent.bindings}, ipam: &ipamConfigurationExecutor{client: c, intent: intent}}, nil
}

type siteConfigurationExecutor struct {
	applied            map[string]ciskov1.NetworkControllerConfigurationActionApplication
	comparison         *templateInputComparison
	privateResource    func(desiredSite) string
	creators           map[string]siteCreatorRecord
	creatorOwner       string
	submissionGuard    func(context.Context, string, string, configurationResourceIdentity) error
	client             *client
	desired            []desiredSite
	bulkCreate         bool
	bulkGuard          func(context.Context, string, string) error
	verifyEffects      func(context.Context, *ciskov1.NetworkControllerConfigurationOperation) ([]string, error)
	effectRecordsMatch func(*ciskov1.NetworkControllerConfigurationStatus) error
}

func (e *siteConfigurationExecutor) count() int { return len(e.desired) }
func (e *siteConfigurationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	return e.nextObserved(ctx, actual)
}
func (e *siteConfigurationExecutor) nextObserved(ctx context.Context, actual []Site) (*configurationWrite, error) {
	if err := e.checkCreatorIdentities(actual); err != nil {
		return nil, err
	}
	if err := e.checkPrivateBaselines(ctx, actual); err != nil {
		return nil, err
	}
	if e.bulkCreate {
		plan, err := planSiteBulkCreate(e.desired, actual)
		if err != nil && !errors.Is(err, errConfigurationBusy) {
			return nil, err
		}
		if err == nil && plan != nil {
			return e.bulkWrite(ctx, actual)
		}
	}
	m, err := nextSiteMutation(e.desired, actual)
	if err != nil || m == nil {
		return nil, err
	}
	return e.singularWrite(ctx, actual, m)
}
func (e *siteConfigurationExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if strings.HasPrefix(op.Resource, "site-bulk-create:") {
		return e.verifyBulk(ctx, op)
	}
	intended, _ := siteDesiredOperation(e.desired, op.Resource)
	if intended == nil || !validAPIID(op.ParentID) || (op.Action == "Update" && !validAPIID(op.TargetID)) || (op.Action != "Create" && op.Action != "Update") {
		return false, errConfigurationTransition
	}
	return e.client.verifySiteMutation(ctx, &siteMutation{desired: *intended, id: op.TargetID, parentID: op.ParentID})
}

// Groups compose resource planners only. They share the existing journal and
// preserve a collision-free resource namespace without adding a state machine.
type configurationPart struct {
	name     string
	executor configurationExecutor
}
type configurationGroup struct{ parts []configurationPart }

func (g *configurationGroup) count() int {
	n := 0
	for _, p := range g.parts {
		n += p.executor.count()
	}
	return n
}
func (g *configurationGroup) next(ctx context.Context) (*configurationWrite, error) {
	var first *configurationWrite
	for _, part := range g.parts {
		write, err := part.executor.next(ctx)
		if err != nil {
			return nil, err
		}
		if write != nil {
			copy := *write
			copy.resource = part.name + "/" + write.resource
			if len(copy.resource) > 512 {
				return nil, errors.New("configuration resource exceeds journal bound")
			}
			if first == nil {
				first = &copy
			}
		}
	}
	return first, nil
}
func (g *configurationGroup) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	for _, p := range g.parts {
		prefix := p.name + "/"
		if strings.HasPrefix(op.Resource, prefix) {
			copy := *op
			copy.Resource = strings.TrimPrefix(op.Resource, prefix)
			return p.executor.verify(ctx, &copy)
		}
	}
	return false, errConfigurationTransition
}
