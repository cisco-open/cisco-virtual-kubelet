// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CancelMetadataHandoff competes with the original worker claim on the SAME
// resourceVersion. Only untouched, ready Prepared custody can be cancelled.
// A lost CAS acknowledgement is reread by the caller; Submitting/Unknown cannot
// be converted into non-dispatch proof. No Lease is released here.
func CancelMetadataHandoff(ctx context.Context, reader client.Reader, writer client.Client, expected *cvk.NetworkController, installation BootstrapFenceInstallation, policy string, normalize MetadataSettlementProjection, authorize MetadataSettlementRuntimeCheck, now time.Time) (*cvk.NetworkController, error) {
	if expected == nil || expected.Status.Configuration == nil || expected.Status.Configuration.MetadataHandoff == nil {
		return nil, errors.New("prepared metadata custody is unavailable")
	}
	expected = expected.DeepCopy()
	h := expected.Status.Configuration.MetadataHandoff
	op := expected.Status.Configuration.Operation
	if op == nil || op.Phase != "Prepared" || op.SubmittedAt != nil || !reflect.DeepEqual(*op, h.Operation) || h.CancelledAt != nil || h.RefusedAt != nil || h.RefusedSubmissionAt != nil || h.SettlementDigest != "" || h.ReleasedAt != nil {
		return nil, errors.New("metadata custody is claimed, terminal or uncertain")
	}
	s, err := newMetadataSettlement(ctx, reader, writer, expected, installation, policy, normalize, authorize, now)
	if err != nil {
		return nil, err
	}
	if err := s.bindOriginal(ctx); err != nil {
		return nil, err
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	updated := s.current.DeepCopy()
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	if at.Before(h.ReadyAt) {
		return nil, errors.New("cancellation predates original readiness")
	}
	updated.Status.Configuration.Operation = nil
	updated.Status.Configuration.MetadataHandoff.CancelledAt = &at
	if err := s.persist(ctx, writer, updated); err != nil {
		return nil, err
	}
	return s.current.DeepCopy(), nil
}
