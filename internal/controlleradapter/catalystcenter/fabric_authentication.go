// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Parsing/reporting does not authorize physical fabric changes. The generic
// metadata executor refuses the entire composition, including earlier writes.
// The public reconciler uses its explicit group preparation/recovery path.
type fabricAuthConfiguration struct {
	client     *client
	foundation configurationExecutor
	desired    []fabricAuthIntent
}

func (e *fabricAuthConfiguration) count() int { return e.foundation.count() + len(e.desired) }
func (e *fabricAuthConfiguration) next(context.Context) (*configurationWrite, error) {
	return nil, errConfigurationDeviceAdmission
}
func (e *fabricAuthConfiguration) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	return false, errConfigurationDeviceAdmission
}

type fabricAuthState struct {
	want             fabricAuthIntent
	siteID, fabricID string
	profile          authProfile
	exists           bool
}

func (c *client) observeFabricAuthentication(ctx context.Context, desired []fabricAuthIntent) ([]fabricAuthState, error) {
	sites := make([]fabricSiteIntent, len(desired))
	for i, w := range desired {
		sites[i] = w.site
	}
	observations, err := (&fabricSiteExecutor{client: c, desired: sites}).observe(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fabricAuthState, 0, len(desired))
	for i, o := range observations {
		if !validAPIID(o.siteID) {
			return nil, errInvalidResponse
		}
		state := fabricAuthState{want: desired[i], siteID: o.siteID}
		if o.exists {
			if !fabricSiteMatches(o.want, o.actual) {
				return nil, errConfigurationTransition
			}
			state.fabricID = o.actual.ID
			profiles, err := c.listAuthProfiles(ctx, state.fabricID)
			if err != nil {
				return nil, err
			}
			for _, p := range profiles {
				if p.Name == state.want.profile.Name {
					state.profile, state.exists = p, true
				}
			}
			// A fabric's selected built-in profile must already exist. Creating a
			// replacement or adopting a global result would lose native identity.
			if !state.exists {
				return nil, errConfigurationBusy
			}
		}
		out = append(out, state)
	}
	return out, nil
}

func fabricAuthStatesEqual(a, b []fabricAuthState) bool {
	if len(a) != len(b) {
		return false
	}
	for i, left := range a {
		right := b[i]
		if left.siteID != right.siteID || left.fabricID != right.fabricID || left.exists != right.exists || left.profile.ID != right.profile.ID || !authProfilesEqual(left.profile, right.profile) || !authPreservedFieldsEqual(left.profile, right.profile) {
			return false
		}
	}
	return true
}

func (e *fabricAuthConfiguration) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	before, err := e.client.observeFabricAuthentication(ctx, e.desired)
	if err != nil {
		return nil, err
	}
	after, err := e.client.observeFabricAuthentication(ctx, e.desired)
	if err != nil {
		return nil, err
	}
	if !fabricAuthStatesEqual(before, after) {
		return nil, errConfigurationStale
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, o := range before {
		if o.exists && authProfilesEqual(o.want.profile, o.profile) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if o.exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: o.want.resource(o.siteID), Path: "/fabric_sites/authentication_template", Change: change})
	}
	return out, nil
}

// Private preparation primitive. The caller must establish source/owner and
// physical admission/lease authority; this is intentionally not a public path.
// All mutation and response handling reuse the global authentication executor.
func (c *client) planFabricAuthentication(ctx context.Context, desired []fabricAuthIntent, authority func(context.Context) error) (*configurationWrite, error) {
	return c.planFabricAuthenticationWithDispatch(ctx, desired, authority, nil)
}

func (c *client) planFabricAuthenticationWithDispatch(ctx context.Context, desired []fabricAuthIntent, authority, beforeSubmit func(context.Context) error) (*configurationWrite, error) {
	if authority == nil || len(desired) == 0 {
		return nil, errConfigurationDeviceAdmission
	}
	frozen := make([]fabricAuthIntent, len(desired))
	seen := map[string]bool{}
	for i, w := range desired {
		if !validSitePath(w.site.path) || w.site.path == "Global" || seen[w.site.path] || w.site.authentication != w.profile.Name || w.profile.BPDU != nil && w.profile.Name != "Closed Authentication" {
			return nil, errInvalidResponse
		}
		seen[w.site.path] = true
		frozen[i].site = w.site
		raw, err := json.Marshal(w.profile)
		if err != nil || json.Unmarshal(raw, &frozen[i].profile) != nil || !validAuthProfile(frozen[i].profile) {
			return nil, errInvalidResponse
		}
	}
	if err := authority(ctx); err != nil {
		return nil, err
	}
	before, err := c.observeFabricAuthentication(ctx, frozen)
	if err != nil {
		return nil, err
	}
	for _, state := range before {
		if !state.exists {
			return nil, errConfigurationBusy
		}
	}
	guard := func(ctx context.Context) error {
		if err := authority(ctx); err != nil {
			return err
		}
		current, err := c.observeFabricAuthentication(ctx, frozen)
		if err != nil {
			return err
		}
		if !fabricAuthStatesEqual(before, current) {
			return errConfigurationStale
		}
		return nil
	}
	for _, state := range before {
		core := &authConfigurationExecutor{client: c, desired: []authProfile{state.want.profile}, fabricID: state.fabricID, guard: guard, beforeSubmit: beforeSubmit}
		write, err := core.next(ctx)
		if err != nil {
			return nil, err
		}
		if write == nil {
			continue
		}
		if write.identity.target != state.profile.ID {
			return nil, errConfigurationStale
		}
		write.resource = state.want.resource(state.siteID)
		return write, nil
	}
	return nil, nil
}

func (c *client) verifyFabricAuthentication(ctx context.Context, desired []fabricAuthIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Update" || !validAPIID(op.ParentID) || !validAPIID(op.TargetID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	// Restrict readback to the accepted source pair; later missing prerequisites
	// cannot prevent verification of the already acknowledged task.
	var selected *fabricAuthIntent
	for _, w := range desired {
		if strings.HasPrefix(op.Resource, w.resourcePrefix()) {
			if selected != nil {
				return false, errConfigurationTransition
			}
			copy := w
			selected = &copy
		}
	}
	if selected == nil {
		return false, errConfigurationStale
	}
	guard := func(ctx context.Context) error {
		states, err := c.observeFabricAuthentication(ctx, []fabricAuthIntent{*selected})
		if err != nil {
			return err
		}
		if len(states) != 1 || !states[0].exists || selected.resource(states[0].siteID) != op.Resource || states[0].fabricID != op.ParentID || states[0].profile.ID != op.TargetID {
			return errConfigurationStale
		}
		return nil
	}
	coreOp := *op
	coreOp.Resource = selected.profile.Name
	core := &authConfigurationExecutor{client: c, desired: []authProfile{selected.profile}, fabricID: op.ParentID, guard: guard}
	return core.verify(ctx, &coreOp)
}

// Keep compile-time checking of the public gate alongside the shared contract.
var _ configurationExecutor = (*fabricAuthConfiguration)(nil)
