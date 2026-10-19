// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"
)

// Called only after the entire cleanup composition has no remaining native
// writes. Reservations are retired independently by the existing manager. This
// hook removes obsolete ownership evidence once that retirement is observable.
func (s *rmaInventoryStages) finalizeCleanup(ctx context.Context) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	for _, entry := range s.entries {
		cleanup, ok := entry.(*deviceReplacementUnmarkExecutor)
		if !ok {
			if _, mark := entry.(*deviceReplacementMarkExecutor); mark {
				continue
			}
			return errConfigurationTransition
		}
		if err := cleanup.finalize(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *deviceReplacementUnmarkExecutor) finalize(ctx context.Context) error {
	if e == nil || e.adapter == nil {
		return errConfigurationTransition
	}
	ref, want, err := e.intent()
	if err != nil {
		return err
	}
	nc, err := e.adapter.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return errConfigurationBusy
	}
	snapshot, err := e.adapter.client.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return err
	}
	receipt, err := deviceReplacementUnmarkApplied(nc.Status.Configuration, e.config, snapshot, time.Now())
	if err != nil {
		return err
	}
	if receipt == nil {
		return errConfigurationBusy
	}
	return e.adapter.retireDeviceReplacementMarkReceipt(ctx, e.config, e.source, e.name, receipt.Operation.ID)
}
