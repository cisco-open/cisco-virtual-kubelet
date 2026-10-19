// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Prepare only the first pending batch after preflighting the complete declared
// sequence, including batches suppressed by current owned receipts. Submission
// closures are discarded; native dispatch still requires the shared one-use
// permit. No reservation, claim, grant or completion is created here.
func (a *adapter) prepareOwnedWiredBulkSequence(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, batches []wiredBulkBatch, now time.Time) (*cvk.NetworkControllerConfigurationOperation, error) {
	planned, err := a.planOwnedWiredBulkSequence(ctx, config, source, batches, func(context.Context, wiredBulkBatch, []Device) error {
		// Preparation must never invoke any of the native mutation closures.
		return errConfigurationTransition
	}, now)
	if err != nil {
		return nil, err
	}
	for _, entry := range planned {
		if entry.write == nil {
			continue
		}
		identity := entry.write.identity
		identity.groupTargets = entry.targets
		return a.prepareConfiguration(ctx, config, source, "inventory", entry.write.resource, entry.write.action, identity)
	}
	return nil, nil
}
