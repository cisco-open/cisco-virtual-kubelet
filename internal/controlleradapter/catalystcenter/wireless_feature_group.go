// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
)

// Reuse the existing group namespace, counts and verification. Feature children
// also provide the selected snapshot check from their original planning pass,
// including when already converged. This avoids an extra inventory pass at rest.
type wirelessFeatureGroup struct{ *configurationGroup }

func (g *wirelessFeatureGroup) next(ctx context.Context) (*configurationWrite, error) {
	if g.configurationGroup == nil || len(g.parts) != 2 {
		return nil, errConfigurationTransition
	}
	checks := []func(context.Context) error{}
	seen := map[string]bool{}
	selected := -1
	var first *configurationWrite
	for index, part := range g.parts {
		if seen[part.name] {
			return nil, errConfigurationTransition
		}
		seen[part.name] = true
		var write *configurationWrite
		var check func(context.Context) error
		var err error
		switch part.name {
		case "cleanair":
			e, ok := part.executor.(*wirelessCleanAirExecutor)
			if !ok {
				return nil, errConfigurationTransition
			}
			write, check, err = e.prepareFeature(ctx)
		case "rrm_fra":
			e, ok := part.executor.(*wirelessFRAExecutor)
			if !ok {
				return nil, errConfigurationTransition
			}
			write, check, err = e.prepareFeature(ctx)
		default:
			return nil, errConfigurationTransition
		}
		if err != nil {
			return nil, err
		}
		if check == nil {
			return nil, errConfigurationTransition
		}
		checks = append(checks, check)
		if write != nil && first == nil {
			copy := *write
			copy.resource = part.name + "/" + copy.resource
			if len(copy.resource) > 512 {
				return nil, errors.New("configuration resource exceeds journal bound")
			}
			first = &copy
			selected = index
		}
	}
	guardWirelessDefinitionPreflight(first, func(ctx context.Context) error {
		// The selected child's guarded submit checks its own snapshot last. Other
		// children must still match the exact planning observations first.
		for index, check := range checks {
			if index != selected {
				if err := check(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return first, nil
}
