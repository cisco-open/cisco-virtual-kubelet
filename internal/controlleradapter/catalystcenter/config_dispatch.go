// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"sync/atomic"
)

// guardConfigurationWrite consumes a planned submission even when preflight
// fails or a receipt is lost. Durable reconciliation owns recovery. This does
// not prove a journaled operation was never dispatched or authorize replay.
func guardConfigurationWrite(write *configurationWrite, unchanged func(context.Context) error) {
	if write == nil {
		return
	}
	if write.submit == nil && write.submitCompleted == nil && write.submitCreated != nil {
		create := write.submitCreated
		var used atomic.Bool
		write.submitCreated = func(ctx context.Context) (string, error) {
			if used.Swap(true) {
				return "", errConfigurationTransition
			}
			if err := unchanged(ctx); err != nil {
				return "", err
			}
			return create(ctx)
		}
		return
	}
	if write.submit == nil && write.submitCompleted != nil {
		complete := write.submitCompleted
		var used atomic.Bool
		write.submitCompleted = func(ctx context.Context) error {
			if used.Swap(true) {
				return errConfigurationTransition
			}
			if err := unchanged(ctx); err != nil {
				return err
			}
			return complete(ctx)
		}
		return
	}
	submit := write.submit
	var used atomic.Bool
	write.submit = func(ctx context.Context) (Task, error) {
		if used.Swap(true) {
			return Task{}, errConfigurationTransition
		}
		if err := unchanged(ctx); err != nil {
			return Task{}, err
		}
		return submit(ctx)
	}
}
