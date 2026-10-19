// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
)

// Feature definitions share summary identities and one-use dispatch, while each
// reader retains its own native field validation and optional-field semantics.
type wirelessFeatureObservation[T any] struct {
	Instance wirelessFeatureInstance
	Detail   *T
}

func wirelessFeatureSnapshot[T any](ctx context.Context, c *client, kind string, names []string, read func(context.Context, wirelessFeatureInstance) (T, error)) (map[string]wirelessFeatureObservation[T], error) {
	instances, err := c.wirelessFeatureInstances(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessFeatureObservation[T]{}
	for _, name := range names {
		if instance, exists := instances[name]; exists {
			detail, err := read(ctx, instance)
			if err != nil {
				return nil, err
			}
			out[name] = wirelessFeatureObservation[T]{Instance: instance, Detail: &detail}
		}
	}
	return out, nil
}
