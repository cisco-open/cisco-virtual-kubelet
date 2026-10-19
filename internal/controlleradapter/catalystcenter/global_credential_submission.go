// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sync/atomic"
)

// Freeze only selected, non-secret native metadata. All selected definitions
// must still match the planning observation immediately before submission,
// including definitions later in the same model. Unrelated credentials do not
// invalidate the write. Source and credential projection checks remain the
// responsibility of the common configuration journal.
func (e *globalCredentialExecutor) guardGlobalCredentialWrite(write *configurationWrite, baseline map[string]map[string]globalCredentialMetadata) *configurationWrite {
	if write == nil {
		return nil
	}
	type selection struct {
		model, name string
		present     bool
		metadata    globalCredentialMetadata
	}
	selected := make([]selection, 0, len(e.desired))
	for _, want := range e.desired {
		got, present := baseline[want.kind.model][want.metadata.Name]
		if got.Port != nil {
			port := *got.Port
			got.Port = &port
		}
		selected = append(selected, selection{want.kind.model, want.metadata.Name, present, got})
	}
	client, submit := e.client, write.submit
	var used atomic.Bool
	write.submit = func(ctx context.Context) (Task, error) {
		// A lost acknowledgement is not permission to repeat a credential write.
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		for range 2 {
			actual, err := client.globalCredentials(ctx)
			if err != nil {
				return Task{}, err
			}
			for _, item := range selected {
				got, present := actual[item.model][item.name]
				if present != item.present || !reflect.DeepEqual(got, item.metadata) {
					return Task{}, errConfigurationStale
				}
			}
		}
		return submit(ctx)
	}
	return write
}
