// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (e *networkProfileConfiguration) selectedSwitchingProfile(name string) (*switchingProfileExecutor, bool) {
	for _, part := range e.parts {
		if p, ok := part.executor.(*switchingProfileExecutor); ok && part.name == "profiles" {
			for _, want := range p.desired {
				if want.name == name {
					return &switchingProfileExecutor{client: p.client, desired: []switchingProfileIntent{want}}, true
				}
			}
		}
	}
	return nil, false
}

// Compare selected typed native state on both sides of the original complete
// task tree through observeOwnedProfileMetadataTask. Canonical inherited values
// remain in memory only. Success grants convergence, never creator custody.
func (e *networkProfileConfiguration) observeSwitchingProfileMetadata(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, name string) (any, bool, error) {
	selected, ok := e.selectedSwitchingProfile(name)
	if !ok || !networkProfileMetadataEnvelope(op) || op.Resource != "profiles/"+name {
		return nil, false, errConfigurationTransition
	}
	rows, err := selected.observe(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(rows) != 1 {
		return nil, false, errConfigurationTransition
	}
	o := rows[0]
	if o.binding != op.ParentID && !(legacyEmptySwitchingProfileBinding(op.ParentID) && profileDependencyIdentity(o.desired) == op.ParentID) {
		return nil, false, errSwitchingProfileBinding
	}
	if op.TargetID != "" && o.actual.ID != "" && o.actual.ID != op.TargetID {
		return nil, false, errConfigurationStale
	}
	templates, err := profileTemplateSets(o.actual)
	if err != nil {
		return nil, false, err
	}
	inherited, valid := privateSwitchingProfileSettings(o.actual)
	if !valid {
		return nil, false, errInvalidResponse
	}
	var native any
	if o.native != nil {
		native = o.native.native
	}
	snapshot := struct {
		ID, Name, Namespace, Binding string
		Templates                    map[string][]string
		Inherited                    any
		Native                       any
	}{o.actual.ID, o.actual.Name, o.actual.Namespace, o.binding, templates, inherited, native}
	return snapshot, validAPIID(o.actual.ID) && switchingProfileMatches(o.desired, o.actual), nil
}
