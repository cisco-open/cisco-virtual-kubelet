// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// ClaimJournalGroupForController resolves the full canonical persisted set;
// inventory-derived candidates and caller-selected subsets cannot substitute
// for missing reservations. ClaimGroupForController performs the existing live
// grant/lease/authority checks and one-use claims; this never reconstructs a
// permit from an existing claim, including after an uncertain acknowledgement.
func ClaimJournalGroupForController(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, namespace string, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) (*GroupSubmissionPermit, error) {
	members, err := readJournalGroupReservations(ctx, reader, expected)
	if err != nil {
		return nil, err
	}
	return ClaimGroupForController(ctx, reader, writer, expected, members, namespace, authorize, now)
}
