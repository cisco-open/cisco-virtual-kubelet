// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type networkProfileConfiguration struct {
	configurationGroup
	comparison   *templateInputComparison
	tagPrune     *wirelessTagPruneExecutor
	tags         configurationGroup
	tagExecutors []*wirelessProfileTagExecutor
	reports      []func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
}

func (c *client) compileNetworkProfileConfiguration(raw []byte) (*networkProfileConfiguration, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("network profiles require a section-specific model")
	}
	section, err := wirelessProfileObject(root["network_profiles"], "switching|wireless")
	if err != nil || len(section) == 0 {
		return nil, errWirelessProfileModel
	}
	out := &networkProfileConfiguration{}
	for _, kind := range []string{"switching", "wireless"} {
		value, exists := section[kind]
		if !exists {
			continue
		}
		projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{kind: value}}})
		if kind == "switching" {
			desired, err := parseNetworkProfiles(projection)
			if err != nil {
				return nil, err
			}
			profiles := &switchingProfileExecutor{client: c, desired: desired}
			assignments := []profileSiteIntent{}
			for _, p := range desired {
				assignments = append(assignments, profileSiteIntent{name: p.name, sites: p.sites})
			}
			sites := &profileSiteExecutor{client: c, desired: assignments}
			// Preserve the existing resource prefixes for recovery of switching work.
			out.parts = append(out.parts, configurationPart{name: "profiles", executor: profiles}, configurationPart{name: "sites", executor: sites})
			out.reports = append(out.reports, profiles.report, sites.report)
		} else {
			desired, err := parseWirelessProfiles(projection)
			if err != nil {
				return nil, err
			}
			profiles := &wirelessProfileExecutor{client: c, desired: desired}
			assignments := []profileSiteIntent{}
			for _, p := range desired {
				assignments = append(assignments, profileSiteIntent{name: p.name, sites: p.sites})
			}
			sites := &profileSiteExecutor{client: c, wireless: true, desired: assignments}
			out.parts = append(out.parts, configurationPart{name: "wireless", executor: profiles}, configurationPart{name: "wireless-sites", executor: sites})
			out.reports = append(out.reports, profiles.report, sites.report)
			for _, policy := range []bool{false, true} {
				tags := &wirelessProfileTagExecutor{client: c, desired: desired, policy: policy}
				name := "wireless-site-tags"
				if policy {
					name = "wireless-policy-tags"
				}
				out.tags.parts = append(out.tags.parts, configurationPart{name: name, executor: tags})
				out.tagExecutors = append(out.tagExecutors, tags)
				out.reports = append(out.reports, tags.report)
			}

		}
	}
	return out, nil
}
func (e *networkProfileConfiguration) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	out := []configapi.NetworkControllerDriftEntry{}
	for _, report := range e.reports {
		entries, err := report(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	return out, nil
}

func (e *networkProfileConfiguration) count() int {
	return e.configurationGroup.count() + e.tags.count()
}
func (e *networkProfileConfiguration) next(ctx context.Context) (*configurationWrite, error) {
	var prunes []wirelessTagPruneObservation
	if e.tagPrune != nil {
		var err error
		prunes, err = e.tagPrune.snapshot(ctx)
		if err != nil {
			return nil, err
		}
	}
	for _, tags := range e.tagExecutors {
		if err := tags.preflight(ctx); err != nil {
			return nil, err
		}
	}
	write, err := e.configurationGroup.next(ctx)
	if err != nil {
		return nil, err
	}
	if write != nil {
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			if e.tagPrune != nil {
				fresh, err := e.tagPrune.snapshot(ctx)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(fresh, prunes) {
					return errConfigurationStale
				}
			}
			for _, tags := range e.tagExecutors {
				if err := tags.preflight(ctx); err != nil {
					return err
				}
			}
			return nil
		})
		return write, nil
	}
	write, err = e.tags.next(ctx)
	if err != nil || write != nil {
		return write, err
	}
	if e.tagPrune != nil {
		return e.tagPrune.next(ctx)
	}
	return nil, nil
}
func (e *networkProfileConfiguration) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if wirelessTagBindingMode(op.Resource) != "" && !wirelessTagPruneSelected(op.Resource) {
		_, done, err := e.boundTagReadback(ctx, op)
		return done, err
	}
	if wirelessTagPruneSelected(op.Resource) {
		if e.tagPrune == nil {
			return false, errConfigurationOwned
		}
		return e.tagPrune.verify(ctx, op)
	}
	// Attribute readback can reconstruct the public selector without a key.
	// Receipt publication and retained custody still require the projected key.
	if wirelessTagPrivateSelected(op.Resource) {
		copy := op.DeepCopy()
		copy.Resource = wirelessTagCanonical(op.Resource)
		op = copy
	}
	combined := configurationGroup{parts: append(append([]configurationPart{}, e.parts...), e.tags.parts...)}
	return combined.verify(ctx, op)
}
